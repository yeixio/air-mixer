package apserver

// #include <dns_sd.h>
// #include <arpa/inet.h>
import "C"

import (
	"errors"
	"fmt"
	"unsafe"

	"github.com/yeixio/air-mixer/internal/logger"
)

// Keep this around so we can deregister them at the end.
var services []C.DNSServiceRef

// Register a DNS Service of name, service type stype, with txt record specified, on
// a certain port. This gets added to a list of registered services which can be
// deregistered by calling ServiceDeregister(). I'm fairly sure that closing the program
// causes the socket connection to mDNSResponder to be closed, which will also deregister
// the services.
func ServiceRegister(name, stype string, txt map[string]string, port uint16) error {
	log := logger.GetLogger()
	log.Debugf("ServiceRegister called - name: %s, type: %s, port: %d", name, stype, port)
	
	txtblob, err := mapToBytes(txt)
	if err != nil {
		log.Errorf("Failed to convert TXT record to bytes: %v", err)
		return err
	}
	log.Debugf("TXT record blob length: %d bytes", len(txtblob))

	// Network order is big-endian
	bport := (port&0xff)<<8 + port>>8
	log.Debugf("Port in network byte order: %d (original: %d)", bport, port)

	var ref C.DNSServiceRef
	log.Debugf("Calling DNSServiceRegister with name='%s', type='%s', port=%d", name, stype, port)
	dnserr := C.DNSServiceRegister(
		&ref,                        // DNSServiceRegister
		0,                           // DNSServiceFlags
		0,                           // Interface (0 => all interfaces)
		C.CString(name),             // Service name (const char *)
		C.CString(stype),            // Service type (const char *)
		nil,                         // Domain (const char *) empty => default domain
		nil,                         // Host (const char *), empty => machine default host
		C.uint16_t(bport),           // Port, in network byte order
		C.uint16_t(len(txtblob)),    // TXT Record length
		unsafe.Pointer(&txtblob[0]), // TXT Record
		nil, // Callback on register/fail (TODO: do something here)
		nil) // Application Context
	
	if dnserr != C.kDNSServiceErr_NoError {
		log.Errorf("DNSServiceRegister failed with error code: %d", int(dnserr))
		return errors.New("Could not register service")
	}

	services = append(services, ref)
	log.Infof("✓ mDNS service '%s' registered successfully (type: %s, port: %d)", name, stype, port)
	return nil
}

// Deregister all previously allocated services.
func ServiceDeregister() {
	log := logger.GetLogger()
	log.Debugf("Deregistering %d mDNS services", len(services))
	for i, ref := range services {
		log.Debugf("Deregistering service %d", i+1)
		C.DNSServiceRefDeallocate(ref)
	}
	services = []C.DNSServiceRef{}
	log.Debug("All mDNS services deregistered")
}

// Convert a map to the TXT record format: 1 byte (length) followed by
// the character data (string)
func mapToBytes(txt map[string]string) ([]byte, error) {
	blob := make([]byte, 0)
	for key, val := range txt {
		line := key + "=" + val
		if len(line) > 0xff {
			return nil, errors.New(fmt.Sprintf("Line \"%s\" greater than 255 bytes", line))
		}
		blob = append(blob, byte(len(line)))
		blob = append(blob, []byte(line)...)
	}

	return blob, nil
}
