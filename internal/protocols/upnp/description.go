package upnp

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// errNoAVTransport is returned for a UPnP device that can't play streams,
// such as a media server or a router.
var errNoAVTransport = errors.New("upnp: the device has no AVTransport service, so it can't play streams")

// device is the part of a UPnP device description that matters here.
type device struct {
	DeviceType   string    `xml:"deviceType"`
	FriendlyName string    `xml:"friendlyName"`
	Manufacturer string    `xml:"manufacturer"`
	ModelName    string    `xml:"modelName"`
	UDN          string    `xml:"UDN"`
	Services     []service `xml:"serviceList>service"`
	Devices      []device  `xml:"deviceList>device"`
}

type service struct {
	ServiceType string `xml:"serviceType"`
	ControlURL  string `xml:"controlURL"`
	EventSubURL string `xml:"eventSubURL"`
}

type description struct {
	URLBase string `xml:"URLBase"`
	Device  device `xml:"device"`
}

// renderer is a media renderer found from its description.
type renderer struct {
	Name, Manufacturer, Model string
	AVTransport               string // control URL
	RenderingControl          string // control URL, or ""
}

// fetchDescription reads a device description and finds the renderer's
// services, in the root device or any embedded one (Sonos nests them).
func fetchDescription(ctx context.Context, client *http.Client, location string) (*renderer, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, location, nil)
	if err != nil {
		return nil, err
	}
	res, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("upnp: device description: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("upnp: device description: %s", res.Status)
	}
	var d description
	if err := xml.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&d); err != nil {
		return nil, fmt.Errorf("upnp: device description: %w", err)
	}
	base, err := url.Parse(location)
	if err != nil {
		return nil, err
	}
	if d.URLBase != "" {
		if b, err := url.Parse(strings.TrimSpace(d.URLBase)); err == nil {
			base = b
		}
	}
	r := &renderer{Name: d.Device.FriendlyName, Manufacturer: d.Device.Manufacturer, Model: d.Device.ModelName}
	var walk func(device)
	walk = func(dev device) {
		for _, s := range dev.Services {
			ref, err := url.Parse(strings.TrimSpace(s.ControlURL))
			if err != nil {
				continue
			}
			u := base.ResolveReference(ref).String()
			switch {
			case strings.HasPrefix(s.ServiceType, "urn:schemas-upnp-org:service:AVTransport:") && r.AVTransport == "":
				r.AVTransport = u
			case strings.HasPrefix(s.ServiceType, "urn:schemas-upnp-org:service:RenderingControl:") && r.RenderingControl == "":
				r.RenderingControl = u
			}
		}
		for _, sub := range dev.Devices {
			walk(sub)
		}
	}
	walk(d.Device)
	if r.AVTransport == "" {
		return nil, errNoAVTransport
	}
	return r, nil
}

// Describe reads a renderer's name and model from its description URL.
func Describe(ctx context.Context, location string) (name, model string, err error) {
	r, err := fetchDescription(ctx, http.DefaultClient, location)
	if err != nil {
		return "", "", err
	}
	model = strings.TrimSpace(r.Model)
	if !strings.HasPrefix(strings.ToLower(model), strings.ToLower(r.Manufacturer)) {
		model = strings.TrimSpace(r.Manufacturer + " " + model)
	}
	return r.Name, model, nil
}
