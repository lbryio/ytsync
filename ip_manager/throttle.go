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

const IPCooldownPeriod = 20 * time.Second
const unbanTimeout = 3 * time.Hour

var stopper = stop.New()

type IPPool struct {
	ips     []throttledIP
	lock    *sync.RWMutex
	stopGrp *stop.Group
}

type throttledIP struct {
	IP           string
	UsedForVideo string
	LastUse      time.Time
	Throttled    bool
	InUse        bool
}

var ipPoolInstance *IPPool

func GetIPPool(stopGrp *stop.Group) (*IPPool, error) {
	if ipPoolInstance != nil {
		return ipPoolInstance, nil
	}
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil, errors.Err(err)
	}
	var pool []throttledIP
	for _, address := range addrs {
		if ipnet, ok := address.(*net.IPNet); ok && ipnet.IP.IsGlobalUnicast() {
			if ipnet.IP.To16() != nil && govalidator.IsIPv6(ipnet.IP.String()) && false {
				pool = append(pool, throttledIP{
					IP:      ipnet.IP.String(),
					LastUse: time.Now().Add(-5 * time.Minute),
				})
			} else if ipnet.IP.To4() != nil && govalidator.IsIPv4(ipnet.IP.String()) {
				pool = append(pool, throttledIP{
					IP:      ipnet.IP.String(),
					LastUse: time.Now().Add(-5 * time.Minute),
				})
			}
		}
	}
	ipPoolInstance = &IPPool{
		ips:     pool,
		lock:    &sync.RWMutex{},
		stopGrp: stopGrp,
	}
	ticker := time.NewTicker(10 * time.Second)
	go func() {
		for {
			select {
			case <-stopGrp.Ch():
				return
			case <-ticker.C:
				ipPoolInstance.lock.RLock()
				for _, ip := range ipPoolInstance.ips {
					log.Debugf("IP: %s\tInUse: %t\tVideoID: %s\tThrottled: %t\tLastUse: %.1f", ip.IP, ip.InUse, ip.UsedForVideo, ip.Throttled, time.Since(ip.LastUse).Seconds())
				}
				ipPoolInstance.lock.RUnlock()
			}
		}
	}()
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
			return
		}
	}
	util.SendErrorToSlack("something went wrong while releasing the IP %s as we reached the end of the function", ip)
}

func (i *IPPool) ReleaseAll() {
	i.lock.Lock()
	defer i.lock.Unlock()
	for j, _ := range i.ips {
		if i.ips[j].Throttled {
			continue
		}
		localIP := &i.ips[j]
		localIP.InUse = false
	}
}

func (i *IPPool) SetThrottled(ip string) {
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
		case <-i.stopGrp.Ch():
			unbanTimer.Stop()
		}
	}(tIP)
}

var ErrAllInUse = errors.Base("all IPs are in use, try again")
var ErrAllThrottled = errors.Base("all IPs are throttled")
var ErrResourceLock = errors.Base("error getting next ip, did you forget to lock on the resource?")
var ErrInterruptedByUser = errors.Base("interrupted by user")

func (i *IPPool) nextIP(forVideo string) (*throttledIP, error) {
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
		nextIP.UsedForVideo = forVideo
		return nextIP, nil
	}
	return nil, errors.Err(ErrAllThrottled)
}

func (i *IPPool) GetIP(forVideo string) (string, error) {
	for {
		ip, err := i.nextIP(forVideo)
		if err != nil {
			if errors.Is(err, ErrAllInUse) {
				select {
				case <-i.stopGrp.Ch():
					return "", errors.Err(ErrInterruptedByUser)
				default:
					time.Sleep(5 * time.Second)
					continue
				}
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
