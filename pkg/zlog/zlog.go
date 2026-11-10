package zlog

import (
	"os"

	stackdriver "github.com/tommy351/zap-stackdriver"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var (
	logger = newDefault()
	skip   = logger.WithOptions(zap.AddCallerSkip(1))
)

type Options struct {
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

func Setup(o Options) error {
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
	skip = l.WithOptions(zap.AddCallerSkip(1))
	return nil
}

func L() *zap.Logger { return logger }

func Sync() { _ = logger.Sync() }

func DebugEnabled() bool { return logger.Core().Enabled(zap.DebugLevel) }

func Debug(msg string, fields ...zap.Field) { skip.Debug(msg, fields...) }
func Info(msg string, fields ...zap.Field)  { skip.Info(msg, fields...) }
func Warn(msg string, fields ...zap.Field)  { skip.Warn(msg, fields...) }
func Error(msg string, fields ...zap.Field) { skip.Error(msg, fields...) }
func Fatal(msg string, fields ...zap.Field) { skip.Fatal(msg, fields...) }
