package main

import (
	"os"
	"strconv"
	"strings"

	"github.com/lbryio/ytsync/util"

	"github.com/sirupsen/logrus"
)

func main() {
	if len(os.Args) != 6 {
		logrus.Info(strings.Join(os.Args, ","))
		logrus.Fatal("Not enough arguments: name, claimID, address, blockchainName, claimAmount")
	}
	println("Supporty!")
	lbrycrd, err := util.GetLbrycrdClient(os.Getenv("LBRYCRD_STRING"))
	if err != nil {
		logrus.Fatal(err)
	}
	if lbrycrd == nil {
		logrus.Fatal("Lbrycrd Client is nil")
	}
	amount, err := strconv.ParseFloat(os.Args[5], 64)
	if err != nil {
		logrus.Error(err)
	}
	name := os.Args[1]
	claimid := os.Args[2]
	claimAddress := os.Args[3]
	blockChainName := os.Args[4]
	logrus.Infof("Supporting %s[%s] with %.2f LBC on chain %s at address %s", name, claimid, amount, blockChainName, claimAddress)
	hash, err := lbrycrd.SupportClaim(name, claimid, claimAddress, blockChainName, amount)
	if err != nil {
		logrus.Error(err)
	}
	if hash == nil {
		logrus.Fatal("Tx not created!")
	}
	logrus.Info("Tx: ", hash.String())
}
