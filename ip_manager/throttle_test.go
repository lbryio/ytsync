package ip_manager

import (
	"testing"
)

func TestAll(t *testing.T) {
	pool, err := GetIPPool()
	if err != nil {
		t.Fatal(err)
	}
	ip, err := pool.GetIP()
	if err != nil {
		t.Fatal(err)
	}
	t.Log(ip)
	pool.ReleaseIP(ip)
	ip2, err := pool.GetIP()
	if err != nil {
		t.Fatal(err)
	}
	if ip == ip2 && len(pool.ips) > 1 {
		t.Fatalf("the same IP was returned twice! %s, %s", ip, ip2)
	}
	t.Log(ip2)
	pool.ReleaseIP(ip2)

	for range pool.ips {
		_, err = pool.GetIP()
		if err != nil {
			t.Fatal(err)
		}
	}
	next, err := pool.nextIP()
	if err != nil {
		t.Logf("%s", err.Error())
	} else {
		t.Fatal(next)
	}
}
