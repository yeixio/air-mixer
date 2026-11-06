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
	{Name: "matinee", Kind: "server", Title: "Matinee", Description: "protocols.matineeServer", Fields: []Field{}},

	{Name: "airplay", Kind: "client", Title: "AirPlay", Description: "protocols.airplayClient", Discovery: true, Fields: []Field{
		{Key: "address", Type: "text", Required: true, Label: "common.address", Hint: "clients.addressHint", Placeholder: "192.168.1.100:7000"},
	}},
	{Name: "cast", Kind: "client", Title: "Google Cast", Description: "protocols.castClient", Discovery: true, Fields: []Field{
		{Key: "address", Type: "text", Required: true, Label: "common.address", Hint: "fields.castAddressHint", Placeholder: "192.168.1.40:8009"},
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

// Protocols lists the protocols this build supports, inputs first.
func Protocols() []Protocol {
	out := make([]Protocol, len(protocolCatalog))
	copy(out, protocolCatalog)
	return out
}
