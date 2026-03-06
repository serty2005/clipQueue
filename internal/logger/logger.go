package logger

import (
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"

	"github.com/serty2005/clipqueue/internal/config"
)

var (
	fileLogger    *log.Logger
	consoleLogger *log.Logger
	logFile       *os.File
	initOnce      sync.Once
)

func Init(cfg *config.Config) error {
	var err error

	initOnce.Do(func() {
		if cfg == nil {
			err = os.ErrInvalid
			return
		}

		if cfg.App.Logs {
			logDir := filepath.Join(config.ResolvePath(cfg.App.DataDir), "logs")
			if err = os.MkdirAll(logDir, 0755); err != nil {
				return
			}

			logPath := filepath.Join(logDir, "app.log")
			logFile, err = os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
			if err != nil {
				return
			}

			fileLogger = log.New(logFile, "", log.LstdFlags)
		}

		if cfg.App.Silent {
			consoleLogger = log.New(io.Discard, "", log.LstdFlags)
		} else {
			consoleLogger = log.New(os.Stdout, "", log.LstdFlags)
		}
	})

	return err
}

func Close() {
	if logFile != nil {
		logFile.Close()
	}
}

func Info(format string, v ...interface{}) {
	if consoleLogger != nil {
		consoleLogger.Printf("INFO: "+format, v...)
	}
	if fileLogger != nil {
		fileLogger.Printf("INFO: "+format, v...)
	}
}

func Error(format string, v ...interface{}) {
	if consoleLogger != nil {
		consoleLogger.Printf("ERROR: "+format, v...)
	}
	if fileLogger != nil {
		fileLogger.Printf("ERROR: "+format, v...)
	}
}

func Debug(format string, v ...interface{}) {
	if consoleLogger != nil {
		consoleLogger.Printf("DEBUG: "+format, v...)
	}
	if fileLogger != nil {
		fileLogger.Printf("DEBUG: "+format, v...)
	}
}

func Warn(format string, v ...interface{}) {
	if consoleLogger != nil {
		consoleLogger.Printf("WARN: "+format, v...)
	}
	if fileLogger != nil {
		fileLogger.Printf("WARN: "+format, v...)
	}
}
