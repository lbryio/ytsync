package ipManager

import (
	"github.com/asaskevich/govalidator"
	"github.com/lbryio/ytsync/util"
	log "github.com/sirupsen/logrus"

	"github.com/lbryio/lbry.go/extras/errors"
	"github.com/lbryio/lbry.go/extras/stop"

	"net"
	"sync"
	"time"
)

const IPCooldownPeriod = 25 * time.Second
const unbanTimeout = 3 * time.Hour

var ipv6Pool []string
var ipv4Pool []string
var throttledIPs map[string]bool
var ipInUse map[string]bool
var ipLastUsed map[string]time.Time
var ipMutex sync.Mutex
var stopper = stop.New()

func GetNextIP(ipv6 bool) (string, error) {
	ipMutex.Lock()
	defer ipMutex.Unlock()
	if len(ipv4Pool) < 1 || len(ipv6Pool) < 1 {
		throttledIPs = make(map[string]bool)
		ipInUse = make(map[string]bool)
		ipLastUsed = make(map[string]time.Time)
		addrs, err := net.InterfaceAddrs()
		if err != nil {
			return "", errors.Err(err)
		}

		for _, address := range addrs {
			if ipnet, ok := address.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
				if ipnet.IP.To16() != nil && govalidator.IsIPv6(ipnet.IP.String()) {
					ipv6Pool = append(ipv6Pool, ipnet.IP.String())
					ipLastUsed[ipnet.IP.String()] = time.Now().Add(-IPCooldownPeriod)
				} else if ipnet.IP.To4() != nil && govalidator.IsIPv4(ipnet.IP.String()) {
					ipv4Pool = append(ipv4Pool, ipnet.IP.String())
					ipLastUsed[ipnet.IP.String()] = time.Now().Add(-IPCooldownPeriod)
				}
			}
		}
	}
	nextIP := ""
	if ipv6 {
		nextIP = getLeastUsedIP(ipv6Pool)
	} else {
		nextIP = getLeastUsedIP(ipv4Pool)
	}
	if nextIP == "" {
		return "throttled", errors.Err("all IPs are throttled")
	}
	lastUse := ipLastUsed[nextIP]
	if time.Since(lastUse) < IPCooldownPeriod {
		log.Debugf("The IP %s is too hot, waiting for %.1f seconds before continuing", nextIP, (IPCooldownPeriod - time.Since(lastUse)).Seconds())
		time.Sleep(IPCooldownPeriod - time.Since(lastUse))
	}

	ipInUse[nextIP] = true
	return nextIP, nil
}

func ReleaseIP(ip string) {
	ipMutex.Lock()
	defer ipMutex.Unlock()
	ipLastUsed[ip] = time.Now()
	ipInUse[ip] = false
}

func getLeastUsedIP(ipPool []string) string {
	nextIP := ""
	veryLastUse := time.Now()
	for _, ip := range ipPool {
		isThrottled := throttledIPs[ip]
		if isThrottled {
			continue
		}
		inUse := ipInUse[ip]
		if inUse {
			continue
		}
		lastUse := ipLastUsed[ip]
		if lastUse.Before(veryLastUse) {
			nextIP = ip
			veryLastUse = lastUse
		}
	}
	return nextIP
}

func SetIpThrottled(ip string, stopGrp *stop.Group) {
	ipMutex.Lock()
	isThrottled := throttledIPs[ip]
	if isThrottled {
		return
	}
	throttledIPs[ip] = true
	ipMutex.Unlock()
	util.SendErrorToSlack("%s set to throttled", ip)

	stopper.Add(1)
	go func() {
		defer stopper.Done()
		unbanTimer := time.NewTimer(unbanTimeout)
		select {
		case <-unbanTimer.C:
			ipMutex.Lock()
			throttledIPs[ip] = false
			ipMutex.Unlock()
			util.SendInfoToSlack("%s set back to not throttled", ip)
		case <-stopGrp.Ch():
			unbanTimer.Stop()
		}
	}()
}
