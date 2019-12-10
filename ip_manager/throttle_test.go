package ip_manager

import (
	"testing"
)

func TestAll(t *testing.T) {
	pool, err := GetIPPool()
	if err != nil {
		t.Fatal(err)
	}
	for range pool.ips {
		ip, err := pool.GetIP()
		if err != nil {
			t.Fatal(err)
		}
		t.Log(ip)
	}

	next, err := pool.nextIP()
	if err != nil {
		t.Logf("%s", err.Error())
	} else {
		t.Fatal(next)
	}
}
