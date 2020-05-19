package metrics

import (
	"os"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
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
		hostname = "ytsync-unknown"
	}
	return hostname
}
