package logging

import (
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var Log *zap.Logger
var Sugar *zap.SugaredLogger

func Init() {
	config := zap.NewDevelopmentConfig()
	config.EncoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder

	var err error
	Log, err = config.Build()
	if err != nil {
		panic(err)
	}

	Sugar = Log.Sugar()

}

func Debug(msg string, fields ...interface{}) {
	Sugar.Debugw(msg, fields...)
}

func Info(msg string, fields ...interface{}) {
	Sugar.Infow(msg, fields...)
}

func Warn(msg string, fields ...interface{}) {
	Sugar.Warnw(msg, fields...)
}

func Error(msg string, fields ...interface{}) {
	Sugar.Errorw(msg, fields...)
}

func Fatal(msg string, fields ...interface{}) {
	Sugar.Fatalw(msg, fields...)
}

func Debugf(template string, args ...interface{}) {
	Sugar.Debugf(template, args...)
}

func Infof(template string, args ...interface{}) {
	Sugar.Infof(template, args...)
}

func Warnf(template string, args ...interface{}) {
	Sugar.Warnf(template, args...)
}

func Errorf(template string, args ...interface{}) {
	Sugar.Errorf(template, args...)
}

func Fatalf(template string, args ...interface{}) {
	Sugar.Fatalf(template, args...)
}
