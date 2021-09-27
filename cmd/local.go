package cmd

import "github.com/lbryio/ytsync/v5/local"

func init() {
	local.AddCommand(rootCmd)
}
