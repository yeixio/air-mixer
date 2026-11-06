// Package upnp speaks UPnP AV in both directions: an output that plays on
// a UPnP/DLNA media renderer (Sonos, AV receivers, smart TVs, Kodi), and
// an input that is itself a media renderer, so phone and desktop apps can
// play to Air-Mixer. It includes SSDP, the discovery UPnP uses.
package upnp

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	avTransport       = "urn:schemas-upnp-org:service:AVTransport:1"
	renderingControl  = "urn:schemas-upnp-org:service:RenderingControl:1"
	connectionManager = "urn:schemas-upnp-org:service:ConnectionManager:1"
	mediaRenderer     = "urn:schemas-upnp-org:device:MediaRenderer:1"

	maxSOAP = 256 << 10
)

// arg is one argument of a SOAP action, in order.
type arg struct{ Name, Value string }

// escape returns s with XML's special characters escaped.
func escape(s string) string {
	var b strings.Builder
	xml.EscapeText(&b, []byte(s)) //nolint:errcheck // a strings.Builder doesn't fail
	return b.String()
}

func envelope(body string) []byte {
	return []byte(`<?xml version="1.0" encoding="utf-8"?>` +
		`<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/">` +
		`<s:Body>` + body + `</s:Body></s:Envelope>`)
}

func actionXML(service, action string, args []arg, response bool) string {
	name := action
	if response {
		name += "Response"
	}
	var b strings.Builder
	fmt.Fprintf(&b, `<u:%s xmlns:u="%s">`, name, service)
	for _, a := range args {
		fmt.Fprintf(&b, "<%s>%s</%s>", a.Name, escape(a.Value), a.Name)
	}
	fmt.Fprintf(&b, "</u:%s>", name)
	return b.String()
}

// Error is a UPnP error a device answered with.
type Error struct {
	Code        int
	Description string
}

func (e *Error) Error() string {
	return fmt.Sprintf("upnp: error %d %s", e.Code, e.Description)
}

// call invokes action on a service and returns its out arguments.
func call(ctx context.Context, client *http.Client, controlURL, service, action string, args []arg) (map[string]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, controlURL, bytes.NewReader(envelope(actionXML(service, action, args, false))))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", `text/xml; charset="utf-8"`)
	req.Header.Set("SOAPACTION", `"`+service+"#"+action+`"`)
	res, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("upnp: %s: %w", action, err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, maxSOAP))
	if err != nil {
		return nil, fmt.Errorf("upnp: %s: %w", action, err)
	}
	_, out, fault, err := parseBody(body)
	if err != nil {
		return nil, fmt.Errorf("upnp: %s: %w (HTTP %s)", action, err, res.Status)
	}
	if fault != nil {
		return nil, fmt.Errorf("upnp: %s: %w", action, fault)
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("upnp: %s: %s", action, res.Status)
	}
	return out, nil
}

// parseBody reads a SOAP envelope: the name of the element in its body,
// that element's children, and a UPnP error if it is a fault.
func parseBody(b []byte) (name string, values map[string]string, fault *Error, err error) {
	d := xml.NewDecoder(bytes.NewReader(b))
	values = map[string]string{}
	depth, bodyDepth := 0, -1
	var field string
	var text strings.Builder
	for {
		tok, terr := d.Token()
		if errors.Is(terr, io.EOF) {
			break
		}
		if terr != nil {
			return "", nil, nil, terr
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			switch {
			case t.Name.Local == "Body" && bodyDepth < 0:
				bodyDepth = depth
			case bodyDepth > 0 && depth == bodyDepth+1:
				name = t.Name.Local
			case name == "Fault" && (t.Name.Local == "errorCode" || t.Name.Local == "errorDescription"):
				field = t.Name.Local
				text.Reset()
			case bodyDepth > 0 && depth == bodyDepth+2 && name != "Fault":
				field = t.Name.Local
				text.Reset()
			}
		case xml.CharData:
			if field != "" {
				text.Write(t)
			}
		case xml.EndElement:
			if field != "" && t.Name.Local == field {
				values[field] = text.String()
				field = ""
			}
			depth--
		}
	}
	if name == "" {
		return "", nil, nil, errors.New("no SOAP body")
	}
	if name == "Fault" {
		var code int
		fmt.Sscan(values["errorCode"], &code) //nolint:errcheck // 0 if missing
		return name, nil, &Error{Code: code, Description: values["errorDescription"]}, nil
	}
	return name, values, nil, nil
}
