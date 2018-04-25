package ytsync

import (
	"testing"
)

func TestWaitForDaemonProcess(t *testing.T) {
	/*
		err := startDaemonViaSystemd()
		if err != nil {
			t.Fail()
		}
		log.Infoln("Waiting 5 seconds for the daemon to start...")
		time.Sleep(5 * time.Second)
		err = stopDaemonViaSystemd()
		if err != nil {
			t.Fail()
		}
	*/
	//start lbrynet before running this test
	// stop lbrynet while running this test
	err := waitForDaemonProcess(100)
	if err != nil {
		t.Fail()
	}
}
