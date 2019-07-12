package ipManager

import (
	"github.com/asaskevich/govalidator"
	log "github.com/sirupsen/logrus"

	"github.com/lbryio/lbry.go/extras/errors"
	"github.com/lbryio/lbry.go/extras/stop"

	"net"
	"sync"
	"time"
)

const IPCooldownPeriod = 20 * time.Second
const unbanTimeout = 3 * time.Hour

var ipv6Pool []string
var ipv4Pool []string
var throttledIPs map[string]bool
var ipLastUsed map[string]time.Time
var ipMutex sync.Mutex
var stopper = stop.New()

func SignalShutdown() {
	stopper.Stop()
}

func GetNextIP(ipv6 bool) (string, error) {
	ipMutex.Lock()
	defer ipMutex.Unlock()
	if len(ipv4Pool) < 1 || len(ipv6Pool) < 1 {
		addrs, err := net.InterfaceAddrs()
		if err != nil {
			return "", errors.Err(err)
		}

		for _, address := range addrs {
			if ipnet, ok := address.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
				if ipnet.IP.To16() != nil && govalidator.IsIPv6(ipnet.IP.String()) {
					ipv6Pool = append(ipv6Pool, ipnet.IP.String())
				} else if ipnet.IP.To4() != nil && govalidator.IsIPv4(ipnet.IP.String()) {
					ipv4Pool = append(ipv4Pool, ipnet.IP.String())
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
		time.Sleep(IPCooldownPeriod - time.Since(lastUse))
	}

	ipLastUsed[nextIP] = time.Now()
	return nextIP, nil
}

func getLeastUsedIP(ipPool []string) string {
	nextIP := ""
	veryLastUse := time.Now()
	for _, ip := range ipPool {
		isThrottled, _ := throttledIPs[ip]
		if isThrottled {
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
	defer ipMutex.Unlock()
	isThrottled, _ := throttledIPs[ip]
	if isThrottled {
		return
	}
	throttledIPs[ip] = true
	log.Printf("%s set to throttled", ip)

	stopper.Add(1)
	go func() {
		defer stopper.Done()
		unbanTimer := time.NewTimer(unbanTimeout)
		for {
			select {
			case <-unbanTimer.C:
				throttledIPs[ip] = false
				log.Printf("%s set back to not throttled", ip)
				return
			case <-stopGrp.Ch():
				unbanTimer.Stop()
				return
			default:
				time.Sleep(5 * time.Second)
			}
		}
	}()
}
