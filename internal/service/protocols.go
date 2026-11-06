package service

// Field is one setting of a protocol, for building a form. Label, Hint
// and PlaceholderKey are keys in the web UI's translations
// (i18n/locales/<lang>/web.json); Placeholder is shown as it is.
type Field struct {
	// Key is the config key, or "address" for a client's address.
	Key            string `json:"key"`
	Type           string `json:"type"` // "text", "number", "url" or "bool"
	Required       bool   `json:"required,omitempty"`
	Label          string `json:"label"`
	Hint           string `json:"hint,omitempty"`
	Placeholder    string `json:"placeholder,omitempty"`
	PlaceholderKey string `json:"placeholder_key,omitempty"`
}

// Protocol describes a protocol adapter: what it is called, which fields
// it takes and whether devices can be discovered for it.
type Protocol struct {
	Name string `json:"name"`
	Kind string `json:"kind"` // "server" (input) or "client" (output)
	// Title is the protocol's name, as it stays in every language.
	Title string `json:"title"`
	// Description is the web UI translation key of a one-line summary.
	Description string  `json:"description"`
	Fields      []Field `json:"fields"`
	Discovery   bool    `json:"discovery,omitempty"`
	// Requires names a program the protocol runs, and Available whether
	// it is installed.
	Requires  string `json:"requires,omitempty"`
	Available bool   `json:"available"`
}

var (
	fieldInterface = Field{
		Key: "interface", Type: "text", Label: "servers.interface", Hint: "servers.interfaceHint",
		PlaceholderKey: "servers.interfacePlaceholder",
	}
	fieldPulseServer = Field{
		Key: "server", Type: "text", Label: "fields.pulseServer", Hint: "fields.pulseServerHint",
		Placeholder: "unix:/run/user/1000/pulse/native",
	}
	fieldDelay = Field{Key: "delay_ms", Type: "number", Label: "fields.delay", Hint: "fields.delayHint", Placeholder: "0"}
)

// protocolCatalog lists every built-in protocol, inputs first, in the order
// the web UI offers them. Factory builds each of them.
var protocolCatalog = []Protocol{
	{Name: "airplay", Kind: "server", Title: "AirPlay", Description: "protocols.airplayServer", Fields: []Field{fieldInterface}},
	{Name: "radio", Kind: "server", Title: "Internet radio", Description: "protocols.radioServer", Fields: []Field{
		{Key: "url", Type: "url", Required: true, Label: "fields.url", Hint: "fields.urlHint", Placeholder: "https://stream.example.com/radio.mp3"},
	}},
	{Name: "pulse", Kind: "server", Title: "PipeWire / PulseAudio", Description: "protocols.pulseServer", Fields: []Field{
		{Key: "source", Type: "text", Label: "fields.source", Hint: "fields.sourceHint", Placeholder: "alsa_input.usb-…"},
		fieldPulseServer,
	}},
	{Name: "bluetooth", Kind: "server", Title: "Bluetooth", Description: "protocols.bluetoothServer", Fields: []Field{
		{Key: "device", Type: "text", Label: "fields.btDevice", Hint: "fields.btDeviceHint", Placeholder: "11:22:33:44:55:66"},
		{Key: "adapter", Type: "text", Label: "fields.btAdapter", Placeholder: "hci0"},
		{Key: "hidden", Type: "bool", Label: "fields.btHidden"},
		fieldPulseServer,
	}},
	{Name: "spotify", Kind: "server", Title: "Spotify Connect", Description: "protocols.spotifyServer", Requires: "librespot", Fields: []Field{
		{Key: "bitrate", Type: "number", Label: "fields.bitrate", Hint: "fields.bitrateHint", Placeholder: "320"},
	}},
	{Name: "airplay2", Kind: "server", Title: "AirPlay 2", Description: "protocols.airplay2Server", Requires: "shairport-sync", Fields: []Field{
		{Key: "address", Type: "text", Label: "fields.listen", Hint: "fields.airplay2PortHint", Placeholder: ":7000"},
	}},
	{Name: "upnp", Kind: "server", Title: "UPnP/DLNA", Description: "protocols.upnpServer", Fields: []Field{
		{Key: "address", Type: "text", Label: "fields.listen", Hint: "fields.listenHint", Placeholder: ":49494"},
	}},
	{Name: "matinee", Kind: "server", Title: "Matinee", Description: "protocols.matineeServer", Fields: []Field{}},

	{Name: "airplay", Kind: "client", Title: "AirPlay", Description: "protocols.airplayClient", Discovery: true, Fields: []Field{
		{Key: "address", Type: "text", Required: true, Label: "common.address", Hint: "clients.addressHint", Placeholder: "192.168.1.100:7000"},
	}},
	{Name: "airplay2", Kind: "client", Title: "AirPlay 2", Description: "protocols.airplay2Client", Discovery: true, Fields: []Field{
		{Key: "address", Type: "text", Required: true, Label: "common.address", Hint: "fields.airplay2AddressHint", Placeholder: "192.168.1.60:7000"},
	}},
	{Name: "cast", Kind: "client", Title: "Google Cast", Description: "protocols.castClient", Discovery: true, Fields: []Field{
		{Key: "address", Type: "text", Required: true, Label: "common.address", Hint: "fields.castAddressHint", Placeholder: "192.168.1.40:8009"},
	}},
	{Name: "upnp", Kind: "client", Title: "UPnP/DLNA", Description: "protocols.upnpClient", Discovery: true, Fields: []Field{
		{Key: "address", Type: "text", Required: true, Label: "common.address", Hint: "fields.upnpAddressHint", Placeholder: "http://192.168.1.50:1400/xml/device_description.xml"},
		{Key: "format", Type: "text", Label: "fields.format", Hint: "fields.formatHint", Placeholder: "wav"},
	}},
	{Name: "pulse", Kind: "client", Title: "PipeWire / PulseAudio", Description: "protocols.pulseClient", Fields: []Field{
		{Key: "sink", Type: "text", Label: "fields.sink", Hint: "fields.sinkHint", Placeholder: "AA:BB:CC:DD:EE:FF"},
		fieldPulseServer, fieldDelay,
	}},
	{Name: "snapcast", Kind: "client", Title: "Snapcast", Description: "protocols.snapcastClient", Discovery: true, Fields: []Field{
		{Key: "address", Type: "text", Required: true, Label: "common.address", Hint: "fields.snapcastAddressHint", Placeholder: "snapserver.local:4953"},
		{Key: "sample_rate", Type: "number", Label: "fields.sampleRate", Hint: "fields.sampleRateHint", Placeholder: "48000"},
	}},
	{Name: "matinee", Kind: "client", Title: "Matinee", Description: "protocols.matineeClient", Fields: []Field{
		{Key: "address", Type: "text", Required: true, Label: "common.address"},
	}},
	{Name: "stream", Kind: "client", Title: "Stream", Description: "protocols.streamClient", Fields: []Field{}},
	{Name: "local", Kind: "client", Title: "Local", Description: "protocols.localClient", Fields: []Field{fieldDelay}},
}

// Protocols lists the protocols this build supports, inputs first, and
// whether the programs they need are installed.
func Protocols() []Protocol {
	out := make([]Protocol, len(protocolCatalog))
	copy(out, protocolCatalog)
	for i := range out {
		out[i].Available = out[i].Requires == "" || needProgram(out[i].Requires) == nil
	}
	return out
}
