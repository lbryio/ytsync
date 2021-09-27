package local

import (
	"fmt"

	"github.com/spf13/cobra"
)

func AddCommand(rootCmd *cobra.Command) {
	cmd := &cobra.Command{
		Use:   "local",
		Short: "run a personal ytsync",
		Run:   localCmd,
	}
	//cmd.Flags().StringVar(&cache, "cache", "", "path to cache")
	rootCmd.AddCommand(cmd)

}

func localCmd(cmd *cobra.Command, args []string) {
	fmt.Println("local")
}
