package logging

import (
	"fmt"
	"os"
	"path"
	"runtime"
	"strconv"
	"time"

	"github.com/sirupsen/logrus"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

func InitLogLevel() {
	logLevelString, isSet := os.LookupEnv("PTP_LOG_LEVEL")
	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))

	var logLevel logrus.Level
	var err error

	if !isSet {
		logrus.Infof("PTP_LOG_LEVEL environment not set, defaulting to INFO\nValid values are: trace, debug, info, warn, error, fatal, panic")
		logLevel = logrus.InfoLevel
	} else {
		logLevel, err = logrus.ParseLevel(logLevelString)
		if err != nil {
			logrus.Errorf("PTP_LOG_LEVEL set to invalid value '%s', defaulting to INFO\nValid values are: trace, debug, info, warn, error, fatal, panic", logLevelString)
			logLevel = logrus.InfoLevel
		}
	}

	logrus.Infof("Log level set to: %s", logLevel)
	logrus.SetLevel(logLevel)
	SetLogFormat()
}

// SetLogFormat sets the log format for logrus
func SetLogFormat() {
	customFormatter := new(logrus.TextFormatter)
	customFormatter.TimestampFormat = time.StampMilli
	customFormatter.PadLevelText = true
	customFormatter.FullTimestamp = true
	customFormatter.ForceColors = true
	logrus.SetReportCaller(true)
	customFormatter.CallerPrettyfier = func(f *runtime.Frame) (string, string) {
		_, filename := path.Split(f.File)
		return strconv.Itoa(f.Line) + "]", fmt.Sprintf("[%s:", filename)
	}
	logrus.SetFormatter(customFormatter)
}
