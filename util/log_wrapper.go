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
	err := util.SendToSlack(":sos: ```" + message + "```")
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
	err := util.SendToSlack(":information_source: " + message)
	if err != nil {
		log.Errorln(err)
	}
}
