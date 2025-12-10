package audio

import (
	"sync"
)

// Mixer implements AudioMixer interface
type Mixer struct {
	sources    map[string]<-chan []float32
	volumes    map[string]float32
	output     chan []float32
	mu         sync.RWMutex
	running    bool
	done       chan struct{}
	bufferSize int
}

// NewMixer creates a new audio mixer
func NewMixer(bufferSize int) *Mixer {
	return &Mixer{
		sources:    make(map[string]<-chan []float32),
		volumes:    make(map[string]float32),
		output:     make(chan []float32, bufferSize),
		done:       make(chan struct{}),
		bufferSize: bufferSize,
	}
}

// AddSource adds an audio source to the mixer
func (m *Mixer) AddSource(id string, source <-chan []float32) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.sources[id]; exists {
		return ErrSourceExists
	}

	m.sources[id] = source
	m.volumes[id] = 1.0 // Default volume

	return nil
}

// RemoveSource removes an audio source from the mixer
func (m *Mixer) RemoveSource(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.sources[id]; !exists {
		return ErrSourceNotFound
	}

	delete(m.sources, id)
	delete(m.volumes, id)

	return nil
}

// SetSourceVolume sets the volume for a specific source (0.0 to 1.0)
func (m *Mixer) SetSourceVolume(id string, volume float32) error {
	if volume < 0.0 || volume > 1.0 {
		return ErrInvalidVolume
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.volumes[id]; !exists {
		return ErrSourceNotFound
	}

	m.volumes[id] = volume
	return nil
}

// Output returns the mixed audio stream
func (m *Mixer) Output() <-chan []float32 {
	return m.output
}

// Start starts the mixer
func (m *Mixer) Start() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.running {
		return ErrAlreadyRunning
	}

	m.running = true
	go m.mixLoop()

	return nil
}

// Stop stops the mixer
func (m *Mixer) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.running {
		return nil
	}

	m.running = false
	close(m.done)

	return nil
}

// Close closes the mixer and releases resources
func (m *Mixer) Close() error {
	if err := m.Stop(); err != nil {
		return err
	}
	close(m.output)
	return nil
}

// mixLoop is the main mixing loop
func (m *Mixer) mixLoop() {
	// Create channels for each source with buffering
	sourceChans := make(map[string]chan []float32)
	
	m.mu.RLock()
	for id := range m.sources {
		sourceChans[id] = make(chan []float32, m.bufferSize)
	}
	m.mu.RUnlock()

	// Start goroutines to read from each source
	var wg sync.WaitGroup
	for id, source := range m.sources {
		wg.Add(1)
		go func(sourceID string, src <-chan []float32, dst chan<- []float32) {
			defer wg.Done()
			defer close(dst)
			for {
				select {
				case <-m.done:
					return
				case samples, ok := <-src:
					if !ok {
						return
					}
					select {
					case dst <- samples:
					case <-m.done:
						return
					}
				}
			}
		}(id, source, sourceChans[id])
	}

	// Mixing loop
	for {
		select {
		case <-m.done:
			wg.Wait()
			return
		default:
			m.mu.RLock()
			if len(sourceChans) == 0 {
				m.mu.RUnlock()
				// No sources, output silence
				select {
				case m.output <- make([]float32, m.bufferSize):
				case <-m.done:
					m.mu.RUnlock()
					return
				}
				m.mu.RUnlock()
				continue
			}

			// Collect samples from all sources
			samples := make(map[string][]float32)
			allReady := true
			for id, ch := range sourceChans {
				select {
				case s := <-ch:
					samples[id] = s
				default:
					allReady = false
				}
			}
			m.mu.RUnlock()

			if !allReady || len(samples) == 0 {
				// Not all sources have data, output silence or wait
				continue
			}

			// Mix the samples
			mixed := m.mixSamples(samples)
			select {
			case m.output <- mixed:
			case <-m.done:
				return
			}
		}
	}
}

// mixSamples mixes samples from multiple sources
func (m *Mixer) mixSamples(samples map[string][]float32) []float32 {
	if len(samples) == 0 {
		return make([]float32, m.bufferSize)
	}

	// Find the maximum length
	maxLen := 0
	for _, s := range samples {
		if len(s) > maxLen {
			maxLen = len(s)
		}
	}

	if maxLen == 0 {
		return make([]float32, m.bufferSize)
	}

	// Mix samples
	mixed := make([]float32, maxLen)
	
	m.mu.RLock()
	defer m.mu.RUnlock()

	for id, s := range samples {
		volume := m.volumes[id]
		for i := 0; i < len(s) && i < maxLen; i++ {
			mixed[i] += s[i] * volume
		}
	}

	// Normalize to prevent clipping (simple approach)
	// In production, you might want more sophisticated clipping prevention
	maxVal := float32(0.0)
	for _, v := range mixed {
		if v > maxVal {
			maxVal = v
		}
		if -v > maxVal {
			maxVal = -v
		}
	}
	if maxVal > 1.0 {
		scale := 1.0 / maxVal
		for i := range mixed {
			mixed[i] *= scale
		}
	}

	return mixed
}

// Errors
var (
	ErrSourceExists    = &Error{Message: "source already exists"}
	ErrSourceNotFound  = &Error{Message: "source not found"}
	ErrInvalidVolume   = &Error{Message: "invalid volume (must be 0.0 to 1.0)"}
	ErrAlreadyRunning  = &Error{Message: "mixer is already running"}
)

// Error represents an audio error
type Error struct {
	Message string
}

func (e *Error) Error() string {
	return e.Message
}

