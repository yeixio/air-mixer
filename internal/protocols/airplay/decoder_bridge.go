package airplay

// GetDecoderInfo returns decoder information for API responses
func (s *Server) GetDecoderInfo() map[string]interface{} {
	s.mu.RLock()
	defer s.mu.RUnlock()

	info := map[string]interface{}{
		"has_decoder": s.decoder != nil,
	}

	if s.decoder != nil {
		info["sample_rate"] = s.decoder.SampleRate()
		info["channels"] = s.decoder.Channels()
		info["format"] = string(s.decoder.Format())
	}

	return info
}

