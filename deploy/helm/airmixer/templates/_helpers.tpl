{{/* Chart name. */}}
{{- define "airmixer.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/* Release-qualified name, shortened to leave room for suffixes. */}}
{{- define "airmixer.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 40 | trimSuffix "-" }}
{{- else if contains (include "airmixer.name" .) .Release.Name }}
{{- .Release.Name | trunc 40 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name (include "airmixer.name" .) | trunc 40 | trimSuffix "-" }}
{{- end }}
{{- end }}

{{- define "airmixer.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
app.kubernetes.io/name: {{ include "airmixer.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{- define "airmixer.selectorLabels" -}}
app.kubernetes.io/name: {{ include "airmixer.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{- define "airmixer.image" -}}
{{- printf "%s:%s" .Values.image.repository (default .Chart.AppVersion .Values.image.tag) }}
{{- end }}

{{/* A room with roomDefaults filled in. Call with (dict "root" $ "room" .). */}}
{{- define "airmixer.room" -}}
{{- $room := mergeOverwrite (deepCopy .root.Values.roomDefaults) (deepCopy .room) }}
{{- if not $room.name }}{{ fail "every room needs a name" }}{{ end }}
{{- if not (regexMatch "^[a-z0-9]([-a-z0-9]*[a-z0-9])?$" $room.name) }}{{ fail (printf "room name %q must be a lowercase DNS label, such as living-room; set displayName for the AirPlay name" $room.name) }}{{ end }}
{{- if not $room.speakers }}{{ fail (printf "room %q has no speakers" $room.name) }}{{ end }}
{{- if not $room.displayName }}{{ $_ := set $room "displayName" $room.name }}{{ end }}
{{- if not $room.pulse.socketDir }}{{ $_ := set $room.pulse "socketDir" (printf "/run/user/%v/pulse" $room.pulse.uid) }}{{ end }}
{{- $room | toJson }}
{{- end }}

{{/* Name of a room's resources. Call with (dict "root" $ "room" $room). */}}
{{- define "airmixer.roomName" -}}
{{- printf "%s-room-%s" (include "airmixer.fullname" .root) .room.name | trunc 63 | trimSuffix "-" }}
{{- end }}
