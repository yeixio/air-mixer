package upnp

import (
	"fmt"
	"strings"
)

// The service descriptions (SCPD) of Air-Mixer's media renderer: just
// what control points need to play, pause, stop, seek and set volume.

type stateVar struct {
	Name, Type string
	Values     []string // allowed values, if any
	Events     bool
}

// action lists in arguments as "Name:Var" and out arguments as
// "Name>Var".
type action struct {
	Name string
	Args []string
}

type serviceDesc struct {
	Type, ID, Path string
	Actions        []action
	Vars           []stateVar
}

var services = []serviceDesc{
	{
		Type: avTransport, ID: "urn:upnp-org:serviceId:AVTransport", Path: "AVTransport",
		Actions: []action{
			{"SetAVTransportURI", []string{"InstanceID:A_ARG_TYPE_InstanceID", "CurrentURI:AVTransportURI", "CurrentURIMetaData:AVTransportURIMetaData"}},
			{"SetNextAVTransportURI", []string{"InstanceID:A_ARG_TYPE_InstanceID", "NextURI:NextAVTransportURI", "NextURIMetaData:NextAVTransportURIMetaData"}},
			{"GetMediaInfo", []string{"InstanceID:A_ARG_TYPE_InstanceID", "NrTracks>NumberOfTracks", "MediaDuration>CurrentMediaDuration", "CurrentURI>AVTransportURI", "CurrentURIMetaData>AVTransportURIMetaData", "NextURI>NextAVTransportURI", "NextURIMetaData>NextAVTransportURIMetaData", "PlayMedium>PlaybackStorageMedium", "RecordMedium>RecordStorageMedium", "WriteStatus>RecordMediumWriteStatus"}},
			{"GetTransportInfo", []string{"InstanceID:A_ARG_TYPE_InstanceID", "CurrentTransportState>TransportState", "CurrentTransportStatus>TransportStatus", "CurrentSpeed>TransportPlaySpeed"}},
			{"GetPositionInfo", []string{"InstanceID:A_ARG_TYPE_InstanceID", "Track>CurrentTrack", "TrackDuration>CurrentTrackDuration", "TrackMetaData>CurrentTrackMetaData", "TrackURI>CurrentTrackURI", "RelTime>RelativeTimePosition", "AbsTime>AbsoluteTimePosition", "RelCount>RelativeCounterPosition", "AbsCount>AbsoluteCounterPosition"}},
			{"GetDeviceCapabilities", []string{"InstanceID:A_ARG_TYPE_InstanceID", "PlayMedia>PossiblePlaybackStorageMedia", "RecMedia>PossibleRecordStorageMedia", "RecQualityModes>PossibleRecordQualityModes"}},
			{"GetTransportSettings", []string{"InstanceID:A_ARG_TYPE_InstanceID", "PlayMode>CurrentPlayMode", "RecQualityMode>CurrentRecordQualityMode"}},
			{"GetCurrentTransportActions", []string{"InstanceID:A_ARG_TYPE_InstanceID", "Actions>CurrentTransportActions"}},
			{"Stop", []string{"InstanceID:A_ARG_TYPE_InstanceID"}},
			{"Play", []string{"InstanceID:A_ARG_TYPE_InstanceID", "Speed:TransportPlaySpeed"}},
			{"Pause", []string{"InstanceID:A_ARG_TYPE_InstanceID"}},
			{"Seek", []string{"InstanceID:A_ARG_TYPE_InstanceID", "Unit:A_ARG_TYPE_SeekMode", "Target:A_ARG_TYPE_SeekTarget"}},
			{"Next", []string{"InstanceID:A_ARG_TYPE_InstanceID"}},
			{"Previous", []string{"InstanceID:A_ARG_TYPE_InstanceID"}},
		},
		Vars: []stateVar{
			{Name: "TransportState", Type: "string", Values: []string{"STOPPED", "PLAYING", "PAUSED_PLAYBACK", "TRANSITIONING", "NO_MEDIA_PRESENT"}},
			{Name: "TransportStatus", Type: "string", Values: []string{"OK", "ERROR_OCCURRED"}},
			{Name: "TransportPlaySpeed", Type: "string", Values: []string{"1"}},
			{Name: "NumberOfTracks", Type: "ui4"},
			{Name: "CurrentMediaDuration", Type: "string"},
			{Name: "AVTransportURI", Type: "string"},
			{Name: "AVTransportURIMetaData", Type: "string"},
			{Name: "NextAVTransportURI", Type: "string"},
			{Name: "NextAVTransportURIMetaData", Type: "string"},
			{Name: "PlaybackStorageMedium", Type: "string"},
			{Name: "RecordStorageMedium", Type: "string"},
			{Name: "RecordMediumWriteStatus", Type: "string"},
			{Name: "PossiblePlaybackStorageMedia", Type: "string"},
			{Name: "PossibleRecordStorageMedia", Type: "string"},
			{Name: "PossibleRecordQualityModes", Type: "string"},
			{Name: "CurrentPlayMode", Type: "string", Values: []string{"NORMAL"}},
			{Name: "CurrentRecordQualityMode", Type: "string"},
			{Name: "CurrentTransportActions", Type: "string"},
			{Name: "CurrentTrack", Type: "ui4"},
			{Name: "CurrentTrackDuration", Type: "string"},
			{Name: "CurrentTrackMetaData", Type: "string"},
			{Name: "CurrentTrackURI", Type: "string"},
			{Name: "RelativeTimePosition", Type: "string"},
			{Name: "AbsoluteTimePosition", Type: "string"},
			{Name: "RelativeCounterPosition", Type: "i4"},
			{Name: "AbsoluteCounterPosition", Type: "i4"},
			{Name: "LastChange", Type: "string", Events: true},
			{Name: "A_ARG_TYPE_InstanceID", Type: "ui4"},
			{Name: "A_ARG_TYPE_SeekMode", Type: "string", Values: []string{"REL_TIME", "ABS_TIME", "TRACK_NR"}},
			{Name: "A_ARG_TYPE_SeekTarget", Type: "string"},
		},
	},
	{
		Type: renderingControl, ID: "urn:upnp-org:serviceId:RenderingControl", Path: "RenderingControl",
		Actions: []action{
			{"GetVolume", []string{"InstanceID:A_ARG_TYPE_InstanceID", "Channel:A_ARG_TYPE_Channel", "CurrentVolume>Volume"}},
			{"SetVolume", []string{"InstanceID:A_ARG_TYPE_InstanceID", "Channel:A_ARG_TYPE_Channel", "DesiredVolume:Volume"}},
			{"GetMute", []string{"InstanceID:A_ARG_TYPE_InstanceID", "Channel:A_ARG_TYPE_Channel", "CurrentMute>Mute"}},
			{"SetMute", []string{"InstanceID:A_ARG_TYPE_InstanceID", "Channel:A_ARG_TYPE_Channel", "DesiredMute:Mute"}},
		},
		Vars: []stateVar{
			{Name: "Volume", Type: "ui2"},
			{Name: "Mute", Type: "boolean"},
			{Name: "LastChange", Type: "string", Events: true},
			{Name: "A_ARG_TYPE_InstanceID", Type: "ui4"},
			{Name: "A_ARG_TYPE_Channel", Type: "string", Values: []string{"Master"}},
		},
	},
	{
		Type: connectionManager, ID: "urn:upnp-org:serviceId:ConnectionManager", Path: "ConnectionManager",
		Actions: []action{
			{"GetProtocolInfo", []string{"Source>SourceProtocolInfo", "Sink>SinkProtocolInfo"}},
			{"GetCurrentConnectionIDs", []string{"ConnectionIDs>CurrentConnectionIDs"}},
			{"GetCurrentConnectionInfo", []string{"ConnectionID:A_ARG_TYPE_ConnectionID", "RcsID>A_ARG_TYPE_RcsID", "AVTransportID>A_ARG_TYPE_AVTransportID", "ProtocolInfo>A_ARG_TYPE_ProtocolInfo", "PeerConnectionManager>A_ARG_TYPE_ConnectionManager", "PeerConnectionID>A_ARG_TYPE_ConnectionID", "Direction>A_ARG_TYPE_Direction", "Status>A_ARG_TYPE_ConnectionStatus"}},
		},
		Vars: []stateVar{
			{Name: "SourceProtocolInfo", Type: "string", Events: true},
			{Name: "SinkProtocolInfo", Type: "string", Events: true},
			{Name: "CurrentConnectionIDs", Type: "string", Events: true},
			{Name: "A_ARG_TYPE_ConnectionStatus", Type: "string", Values: []string{"OK", "ContentFormatMismatch", "InsufficientBandwidth", "UnreliableChannel", "Unknown"}},
			{Name: "A_ARG_TYPE_ConnectionManager", Type: "string"},
			{Name: "A_ARG_TYPE_Direction", Type: "string", Values: []string{"Input", "Output"}},
			{Name: "A_ARG_TYPE_ProtocolInfo", Type: "string"},
			{Name: "A_ARG_TYPE_ConnectionID", Type: "i4"},
			{Name: "A_ARG_TYPE_AVTransportID", Type: "i4"},
			{Name: "A_ARG_TYPE_RcsID", Type: "i4"},
		},
	},
}

func (s serviceDesc) scpd() string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="utf-8"?><scpd xmlns="urn:schemas-upnp-org:service-1-0"><specVersion><major>1</major><minor>0</minor></specVersion><actionList>`)
	for _, a := range s.Actions {
		fmt.Fprintf(&b, "<action><name>%s</name><argumentList>", a.Name)
		for _, spec := range a.Args {
			dir, sep := "in", ":"
			if strings.Contains(spec, ">") {
				dir, sep = "out", ">"
			}
			name, v, _ := strings.Cut(spec, sep)
			fmt.Fprintf(&b, "<argument><name>%s</name><direction>%s</direction><relatedStateVariable>%s</relatedStateVariable></argument>", name, dir, v)
		}
		b.WriteString("</argumentList></action>")
	}
	b.WriteString("</actionList><serviceStateTable>")
	for _, v := range s.Vars {
		events := "no"
		if v.Events {
			events = "yes"
		}
		fmt.Fprintf(&b, `<stateVariable sendEvents="%s"><name>%s</name><dataType>%s</dataType>`, events, v.Name, v.Type)
		if len(v.Values) > 0 {
			b.WriteString("<allowedValueList>")
			for _, val := range v.Values {
				fmt.Fprintf(&b, "<allowedValue>%s</allowedValue>", val)
			}
			b.WriteString("</allowedValueList>")
		}
		if v.Name == "Volume" {
			b.WriteString("<allowedValueRange><minimum>0</minimum><maximum>100</maximum><step>1</step></allowedValueRange>")
		}
		b.WriteString("</stateVariable>")
	}
	b.WriteString("</serviceStateTable></scpd>")
	return b.String()
}
