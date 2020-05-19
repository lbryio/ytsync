package metrics

import (
	"os"
	"regexp"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	log "github.com/sirupsen/logrus"
)

var (
	Durations = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "ytsync",
		Subsystem: getHostname(),
		Name:      "duration",
		Help:      "The durations of the individual modules",
	}, []string{"path"})
)

func getHostname() string {
	hostname, err := os.Hostname()
	if err != nil {
		hostname = "ytsync_unknown"
	}
	reg, err := regexp.Compile("[^a-zA-Z0-9_]+")
	if err != nil {
		log.Fatal(err)
	}
	return reg.ReplaceAllString(hostname, "_")
}
