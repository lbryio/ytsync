package ip_manager

import (
	"net"
	"sort"
	"sync"
	"time"

	"github.com/asaskevich/govalidator"
	"github.com/lbryio/lbry.go/v2/extras/errors"
	"github.com/lbryio/lbry.go/v2/extras/stop"
	"github.com/lbryio/ytsync/util"
	log "github.com/sirupsen/logrus"
)

const IPCooldownPeriod = 35 * time.Second
const unbanTimeout = 3 * time.Hour

var stopper = stop.New()

type IPPool struct {
	ips  []throttledIP
	lock *sync.Mutex
}

type throttledIP struct {
	IP        string
	LastUse   time.Time
	Throttled bool
	InUse     bool
}

var ipPoolInstance *IPPool

func GetIPPool() (*IPPool, error) {
	if ipPoolInstance != nil {
		return ipPoolInstance, nil
	}
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil, errors.Err(err)
	}
	var pool []throttledIP
	ipv6Added := false
	for _, address := range addrs {
		if ipnet, ok := address.(*net.IPNet); ok && ipnet.IP.IsGlobalUnicast() {
			if ipnet.IP.To16() != nil && govalidator.IsIPv6(ipnet.IP.String()) && !ipv6Added {
				pool = append(pool, throttledIP{
					IP:      ipnet.IP.String(),
					LastUse: time.Now().Add(-5 * time.Minute),
				})
				ipv6Added = true
			} else if ipnet.IP.To4() != nil && govalidator.IsIPv4(ipnet.IP.String()) {
				pool = append(pool, throttledIP{
					IP:      ipnet.IP.String(),
					LastUse: time.Now().Add(-5 * time.Minute),
				})
			}
		}
	}
	ipPoolInstance = &IPPool{
		ips:  pool,
		lock: &sync.Mutex{},
	}
	return ipPoolInstance, nil
}

// AllThrottled checks whether the IPs provided are all throttled.
// returns false if at least one IP is not throttled
// Not thread safe, should use locking when called
func AllThrottled(ips []throttledIP) bool {
	for _, i := range ips {
		if !i.Throttled {
			return false
		}
	}
	return true
}

// AllInUse checks whether the IPs provided are all currently in use.
// returns false if at least one IP is not in use AND is not throttled
// Not thread safe, should use locking when called
func AllInUse(ips []throttledIP) bool {
	for _, i := range ips {
		if !i.InUse && !i.Throttled {
			return false
		}
	}
	return true
}

func (i *IPPool) ReleaseIP(ip string) {
	i.lock.Lock()
	defer i.lock.Unlock()
	for j, _ := range i.ips {
		localIP := &i.ips[j]
		if localIP.IP == ip {
			localIP.InUse = false
			localIP.LastUse = time.Now()
			break
		}
	}
}

func (i *IPPool) SetThrottled(ip string, stopGrp *stop.Group) {
	i.lock.Lock()
	defer i.lock.Unlock()
	var tIP *throttledIP
	for j, _ := range i.ips {
		localIP := &i.ips[j]
		if localIP.IP == ip {
			if localIP.Throttled {
				return
			}
			localIP.Throttled = true
			tIP = localIP
			break
		}
	}
	util.SendErrorToSlack("%s set to throttled", ip)

	stopper.Add(1)
	go func(tIP *throttledIP) {
		defer stopper.Done()
		unbanTimer := time.NewTimer(unbanTimeout)
		select {
		case <-unbanTimer.C:
			i.lock.Lock()
			tIP.Throttled = false
			i.lock.Unlock()
			util.SendInfoToSlack("%s set back to not throttled", ip)
		case <-stopGrp.Ch():
			unbanTimer.Stop()
		}
	}(tIP)
}

var ErrAllInUse = errors.Base("all IPs are in use, try again")
var ErrAllThrottled = errors.Base("all IPs are throttled")
var ErrResourceLock = errors.Base("error getting next ip, did you forget to lock on the resource?")

func (i *IPPool) nextIP() (*throttledIP, error) {
	i.lock.Lock()
	defer i.lock.Unlock()

	sort.Slice(i.ips, func(j, k int) bool {
		return i.ips[j].LastUse.Before(i.ips[k].LastUse)
	})

	if !AllThrottled(i.ips) {
		if AllInUse(i.ips) {
			return nil, errors.Err(ErrAllInUse)
		}

		var nextIP *throttledIP
		for j, _ := range i.ips {
			ip := &i.ips[j]
			if ip.InUse || ip.Throttled {
				continue
			}
			nextIP = ip
			break
		}
		if nextIP == nil {
			return nil, errors.Err(ErrResourceLock)
		}
		nextIP.InUse = true
		return nextIP, nil
	}
	return nil, errors.Err(ErrAllThrottled)
}

func (i *IPPool) GetIP() (string, error) {
	for {
		ip, err := i.nextIP()
		if err != nil {
			if errors.Is(err, ErrAllInUse) {
				time.Sleep(5 * time.Second)
				continue
			} else if errors.Is(err, ErrAllThrottled) {
				return "throttled", err
			}
			return "", err
		}
		if time.Since(ip.LastUse) < IPCooldownPeriod {
			log.Debugf("The IP %s is too hot, waiting for %.1f seconds before continuing", ip.IP, (IPCooldownPeriod - time.Since(ip.LastUse)).Seconds())
			time.Sleep(IPCooldownPeriod - time.Since(ip.LastUse))
		}
		return ip.IP, nil
	}
}
