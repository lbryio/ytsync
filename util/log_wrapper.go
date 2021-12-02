package util

import (
	"fmt"

	"github.com/lbryio/lbry.go/v2/extras/util"
	log "github.com/sirupsen/logrus"
)

// SendErrorToSlack Sends an error message to the default channel and to the process log.
func SendErrorToSlack(format string, a ...interface{}) {
	message := format
	if len(a) > 0 {
		message = fmt.Sprintf(format, a...)
	}
	log.Errorln(message)
	log.SetLevel(log.InfoLevel) //I don't want to change the underlying lib so this will do...
	err := util.SendToSlack(":sos: ```" + message + "```")
	log.SetLevel(log.DebugLevel)
	if err != nil {
		log.Errorln(err)
	}
}

// SendInfoToSlack Sends an info message to the default channel and to the process log.
func SendInfoToSlack(format string, a ...interface{}) {
	message := format
	if len(a) > 0 {
		message = fmt.Sprintf(format, a...)
	}
	log.Infoln(message)
	log.SetLevel(log.InfoLevel) //I don't want to change the underlying lib so this will do...
	err := util.SendToSlack(":information_source: " + message)
	log.SetLevel(log.DebugLevel)
	if err != nil {
		log.Errorln(err)
	}
}
