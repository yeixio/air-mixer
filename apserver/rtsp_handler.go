package apserver

import (
	"crypto/aes"
	"crypto/cipher"
	"sync"
)

// AudioHandler is a callback function type for handling decoded audio
type AudioHandler func(samples []float32, sampleRate, channels int) error

// SessionData holds session-specific data
type SessionData struct {
	ID          string
	Fmtp        []int
	AESIV       []byte
	AESKey      []byte
	AudioHandler AudioHandler
	mu          sync.RWMutex
}

// SetAudioHandler sets the audio handler for a session
func (sd *SessionData) SetAudioHandler(handler AudioHandler) {
	sd.mu.Lock()
	defer sd.mu.Unlock()
	sd.AudioHandler = handler
}

// GetAudioHandler gets the audio handler for a session
func (sd *SessionData) GetAudioHandler() AudioHandler {
	sd.mu.RLock()
	defer sd.mu.RUnlock()
	return sd.AudioHandler
}

// HandleAudioPacket handles an incoming RTP audio packet
// This decrypts and decodes the ALAC audio
func HandleAudioPacket(session *SessionData, packet []byte) error {
	if len(packet) < 12 {
		return nil // Not a valid RTP packet
	}

	// Extract audio data (skip 12-byte RTP header)
	audio := packet[12:]
	if len(audio) == 0 {
		return nil
	}

	// Decrypt if we have keys
	if len(session.AESKey) > 0 && len(session.AESIV) > 0 {
		todec := make([]byte, len(audio))
		copy(todec, audio)

		block, err := aes.NewCipher(session.AESKey)
		if err != nil {
			return err
		}

		AESDec := cipher.NewCBCDecrypter(block, session.AESIV)
		for len(todec) >= aes.BlockSize {
			AESDec.CryptBlocks(todec[:aes.BlockSize], todec[:aes.BlockSize])
			todec = todec[aes.BlockSize:]
		}

		audio = todec
	}

	// Call audio handler if set
	if handler := session.GetAudioHandler(); handler != nil {
		// For now, we'll need to decode ALAC here or pass raw data
		// This will be handled by the protocol adapter
		// We'll pass the raw ALAC data and let the adapter decode it
		_ = audio // TODO: Decode and call handler
	}

	return nil
}

