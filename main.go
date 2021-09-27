package main

import (
	"math/rand"
	"net/http"
	"time"

	"github.com/lbryio/ytsync/v5/cmd"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	log "github.com/sirupsen/logrus"
)

func main() {
	rand.Seed(time.Now().UnixNano())
	log.SetLevel(log.DebugLevel)

	go func() {
		http.Handle("/metrics", promhttp.Handler())
		log.Error(http.ListenAndServe(":2112", nil))
	}()

	cmd.Execute()
}
