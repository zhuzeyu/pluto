package main

import (
	"fmt"
	"os"

	"gopkg.in/urfave/cli.v1"

	ethUtils "github.com/ethereum/go-ethereum/cmd/utils"
	"github.com/ethereum/go-ethereum/params"

	"github.com/zhuzeyu/pluto/cmd/utils"
	"github.com/zhuzeyu/pluto/version"
)

var (
	// The app that holds all commands and flags.
	app = ethUtils.NewApp(version.Version, "the ethermint command line interface")
	// flags that configure the go-ethereum node
	nodeFlags = []cli.Flag{
		ethUtils.DataDirFlag,
		ethUtils.KeyStoreDirFlag,
		ethUtils.NoUSBFlag,
		ethUtils.FakePoWFlag,
		// Performance tuning
		ethUtils.CacheFlag,
		ethUtils.TrieCacheGenFlag,
		ethUtils.GCModeFlag,
		// Account settings
		ethUtils.UnlockedAccountFlag,
		ethUtils.PasswordFileFlag,
		ethUtils.VMEnableDebugFlag,
		// Logging and debug settings
		ethUtils.NoCompactionFlag,
		// Gas price oracle settings
		ethUtils.GpoBlocksFlag,
		ethUtils.GpoPercentileFlag,
		utils.TargetGasLimitFlag,
		// Gas Price
		//ethUtils.GasPriceFlag,
		ethUtils.MinerGasPriceFlag,
		//network setting
		ethUtils.LightPeersFlag,
		ethUtils.MaxPeersFlag,
	}

	rpcFlags = []cli.Flag{
		ethUtils.RPCEnabledFlag,
		ethUtils.RPCListenAddrFlag,
		ethUtils.RPCPortFlag,
		ethUtils.RPCCORSDomainFlag,
		ethUtils.RPCApiFlag,
		ethUtils.IPCDisabledFlag,
		ethUtils.WSEnabledFlag,
		ethUtils.WSListenAddrFlag,
		ethUtils.WSPortFlag,
		ethUtils.WSApiFlag,
		ethUtils.WSAllowedOriginsFlag,
	}

	// flags that configure the ABCI app
	ethermintFlags = []cli.Flag{
		utils.TendermintAddrFlag,
		utils.ABCIAddrFlag,
		utils.ABCIProtocolFlag,
		utils.VerbosityFlag,
		utils.ConfigFileFlag,
		utils.WithTendermintFlag,
	}

	// flags that configure the ABCI app
	tendermintFlags = []cli.Flag{
		utils.PexReactor,
		utils.PrivValidatorListenAddr,
		utils.PrivValidator,
		utils.FastSync,
		utils.PersistentPeers,
		utils.AddrBook,
		utils.PrivatePeerIDs,
	}
)

func init() {
	app.Action = plutoCmd
	app.HideVersion = true
	app.Commands = []cli.Command{
		{
			Action:      initCmd,
			Name:        "init",
			Usage:       "init genesis.json",
			Description: "Initialize the files",
		},
		{
			Action:      versionCmd,
			Name:        "version",
			Usage:       "",
			Description: "Print the version",
		},
		{
			Action: resetCmd,
			Name:   "unsafe_reset_all",
			Usage:  "(unsafe) Remove ethermint database",
		},
	}

	app.Flags = append(app.Flags, nodeFlags...)
	app.Flags = append(app.Flags, rpcFlags...)
	app.Flags = append(app.Flags, ethermintFlags...)
	app.Flags = append(app.Flags, tendermintFlags...)

	app.Before = func(ctx *cli.Context) error {
		if err := utils.Setup(ctx); err != nil {
			return err
		}

		ethUtils.SetupMetrics(ctx)

		return nil
	}
}

func versionCmd(ctx *cli.Context) error {
	fmt.Println("ethermint: ", version.Version)
	fmt.Println("go-ethereum: ", params.Version)
	return nil
}

func main() {
	if err := app.Run(os.Args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
