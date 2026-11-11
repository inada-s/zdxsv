package main

import (
	"os"

	stackdriver "github.com/tommy351/zap-stackdriver"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var (
	logger = newDefault()
)

type LogOptions struct {
	// Prod writes stackdriver JSON lines; otherwise a console format.
	Prod bool
	// 0 and 1 log from info up, 2 and above also debug.
	Verbosity int
	// A file to append to; empty means stderr.
	Path   string
	Fields []zap.Field
}

func newDefault() *zap.Logger {
	enc := zapcore.NewConsoleEncoder(zap.NewDevelopmentEncoderConfig())
	core := zapcore.NewCore(enc, zapcore.Lock(os.Stderr), zap.InfoLevel)
	return zap.New(core, zap.AddCaller())
}

func setupLogger(o LogOptions) error {
	level := zap.InfoLevel
	if 2 <= o.Verbosity {
		level = zap.DebugLevel
	}
	var enc zapcore.Encoder
	if o.Prod {
		enc = zapcore.NewJSONEncoder(stackdriver.EncoderConfig)
	} else {
		enc = zapcore.NewConsoleEncoder(zap.NewDevelopmentEncoderConfig())
	}
	path := o.Path
	if path == "" {
		path = "stderr"
	}
	sink, _, err := zap.Open(path)
	if err != nil {
		return err
	}
	core := zapcore.NewCore(enc, sink, level)
	l := zap.New(core, zap.AddCaller(), zap.AddStacktrace(zap.ErrorLevel), zap.ErrorOutput(zapcore.Lock(os.Stderr)))
	if len(o.Fields) != 0 {
		l = l.With(o.Fields...)
	}
	logger = l
	return nil
}

func logDebugEnabled() bool { return logger.Core().Enabled(zap.DebugLevel) }
