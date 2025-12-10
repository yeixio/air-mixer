package alac

// Predictor implements linear prediction for ALAC
// ALAC uses linear prediction to reduce sample correlation before entropy coding
type Predictor struct {
	history [4]int32 // Prediction history
}

// NewPredictor creates a new predictor
func NewPredictor() *Predictor {
	return &Predictor{}
}

// Predict predicts the next sample value
// ALAC uses a simple linear predictor
func (p *Predictor) Predict() int32 {
	// Simple linear prediction: predict based on previous samples
	// This is a simplified version - actual ALAC uses more sophisticated prediction
	return p.history[0] - p.history[1] + p.history[2]
}

// Update updates the prediction history with a new sample
func (p *Predictor) Update(sample int32) {
	// Shift history
	p.history[3] = p.history[2]
	p.history[2] = p.history[1]
	p.history[1] = p.history[0]
	p.history[0] = sample
}

// Reset resets the predictor state
func (p *Predictor) Reset() {
	for i := range p.history {
		p.history[i] = 0
	}
}

// Reconstruct reconstructs a sample from a prediction error
func (p *Predictor) Reconstruct(error int32) int32 {
	predicted := p.Predict()
	sample := predicted + error
	p.Update(sample)
	return sample
}

