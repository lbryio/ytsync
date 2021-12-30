package metrics

import (
	"github.com/lbryio/ytsync/v5/configs"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	Durations = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "ytsync",
		Subsystem: configs.Configuration.GetHostname(),
		Name:      "duration",
		Help:      "The durations of the individual modules",
	}, []string{"path"})
)
