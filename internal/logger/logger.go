package logger

import (
	"os"

	"github.com/yeixio/air-mixer/internal/config"
	"github.com/sirupsen/logrus"
)

var log *logrus.Logger

// Init initializes the logger based on configuration
func Init(cfg *config.LoggingConfig) {
	log = logrus.New()
	
	// Set log level
	level, err := logrus.ParseLevel(cfg.Level)
	if err != nil {
		level = logrus.InfoLevel
		log.Warnf("Invalid log level '%s', defaulting to 'info'", cfg.Level)
	}
	log.SetLevel(level)
	
	// Set log format
	if cfg.Format == "json" {
		log.SetFormatter(&logrus.JSONFormatter{})
	} else {
		log.SetFormatter(&logrus.TextFormatter{
			FullTimestamp: true,
		})
	}
	
	// Set output
	log.SetOutput(os.Stdout)
}

// GetLogger returns the initialized logger
func GetLogger() *logrus.Logger {
	if log == nil {
		// Initialize with defaults if not initialized
		log = logrus.New()
		log.SetLevel(logrus.InfoLevel)
		log.SetFormatter(&logrus.TextFormatter{
			FullTimestamp: true,
		})
		log.SetOutput(os.Stdout)
	}
	return log
}

