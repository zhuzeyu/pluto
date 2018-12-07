package main

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/accounts/keystore"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/console"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/tendermint/tendermint/abci/server"
	"github.com/tendermint/tendermint/p2p"
	"github.com/tendermint/tendermint/privval"
	"github.com/tendermint/tendermint/proxy"
	"gopkg.in/urfave/cli.v1"
)

func plutoCmd(ctx *cli.Context) error {
	// Step 1: Setup the go-ethereum node and start it
	node := emtUtils.MakeFullNode(ctx)
	startNode(ctx, node)

	// Setup the ABCI server and start it
	addr := ctx.GlobalString(emtUtils.ABCIAddrFlag.Name)
	abci := ctx.GlobalString(emtUtils.ABCIProtocolFlag.Name)
	blsSelectStrategy := ctx.GlobalBool(emtUtils.TmBlsSelectStrategy.Name)

	ethGenesisJson := ethermintGenesisPath(ctx)
	genesis := utils.ReadGenesis(ethGenesisJson)
	totalBalanceInital := big.NewInt(0)
	for key, _ := range genesis.Alloc {
		totalBalanceInital.Add(totalBalanceInital, genesis.Alloc[key].Balance)
	}

	// Fetch the registered service of this type
	var backend *ethereum.Backend
	if err := node.Service(&backend); err != nil {
		ethUtils.Fatalf("ethereum backend service not running: %v", err)
	}

	// In-proc RPC connection so ABCI.Query can be forwarded over the ethereum rpc
	rpcClient, err := node.Attach()
	if err != nil {
		ethUtils.Fatalf("Failed to attach to the inproc geth: %v", err)
	}

	// Create the ABCI app
	ethApp, err := abciApp.NewEthermintApplication(backend, rpcClient, types.NewStrategy(totalBalanceInital))
	ethApp.GetStrategy().BlsSelectStrategy = blsSelectStrategy
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	ethApp.StartHttpServer()
	ethLogger := tmlog.NewTMLogger(tmlog.NewSyncWriter(os.Stdout)).With("module", "gelchain")
	configLoggerLevel(ctx, &ethLogger)
	ethApp.SetLogger(ethLogger)

	tmConfig := loadTMConfig(ctx)
	ethAccounts, err := types.GetInitialEthAccountFromFile(tmConfig.InitialEthAccountFile())

	genDocFile := tmConfig.GenesisFile()
	genDoc, err := tmState.MakeGenesisDocFromFile(genDocFile)
	if err != nil {
		fmt.Println(err)
	}
	validators := genDoc.Validators
	var tmAddress []string
	amlist := &types.AccountMapList{
		MapList: make(map[string]*types.AccountMap),
	}
	if err != nil {
		panic("Sorry but you don't have initial account file")
	} else {
		fmt.Println(len(ethAccounts.EthAccounts))
		log.Info("get Initial accounts")
		for i := 0; i < len(validators); i++ {
			tmAddress = append(tmAddress, strings.ToLower(hex.EncodeToString(validators[i].PubKey.Address())))
			blsKey := validators[i].BlsPubKey
			blsKeyJsonStr, _ := json.Marshal(blsKey)
			accountBalance := big.NewInt(1)
			accountBalance.Div(totalBalanceInital, big.NewInt(100))
			if i == len(ethAccounts.EthAccounts) {
				break
			}
			amlist.MapList[tmAddress[i]] = &types.AccountMap{
				common.HexToAddress(ethAccounts.EthAccounts[i]),
				ethAccounts.EthBalances[i],
				big.NewInt(0),
				common.HexToAddress(ethAccounts.EthBeneficiarys[i]), //10个eth账户中的第i个。
				string(blsKeyJsonStr),
			}
		}
	}

	ethApp.GetStrategy().SetAccountMapList(amlist)

	// Step 2: If we can invoke `tendermint node`, let's do so
	// in order to make gelchain as self contained as possible.
	// See Issue https://github.com/tendermint/ethermint/issues/244
	canInvokeTendermintNode := canInvokeTendermint(ctx)
	if canInvokeTendermintNode {
		tmConfig := loadTMConfig(ctx)
		clientCreator := proxy.NewLocalClientCreator(ethApp)
		tmLogger := tmlog.NewTMLogger(tmlog.NewSyncWriter(os.Stdout)).With("module", "tendermint")
		configLoggerLevel(ctx, &tmLogger)

		// Generate node PrivKey
		nodeKey, err := p2p.LoadOrGenNodeKey(tmConfig.NodeKeyFile())
		if err != nil {
			return err
		}

		n, err := tmNode.NewNode(tmConfig,
			privval.LoadOrGenFilePV(tmConfig.PrivValidatorFile()),
			nodeKey,
			clientCreator,
			tmNode.DefaultGenesisDocProviderFunc(tmConfig),
			tmNode.DefaultDBProvider,
			tmNode.DefaultMetricsProvider(tmConfig.Instrumentation),
			tmLogger)
		if err != nil {
			log.Info("tendermint newNode", "error", err)
			return err
		}

		backend.SetMemPool(n.MempoolReactor().Mempool)
		n.MempoolReactor().Mempool.SetRecheckFailCallback(backend.Ethereum().TxPool().RemoveTxs)

		err = n.Start()
		if err != nil {
			log.Error("server with tendermint start", "error", err)
			return err
		}
		// Trap signal, run forever.
		n.RunForever()
		return nil
	} else {
		// Start the app on the ABCI server
		srv, err := server.NewServer(addr, abci, ethApp)
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}

		srv.SetLogger(emtUtils.EthermintLogger().With("module", "abci-server"))

		if err := srv.Start(); err != nil {
			fmt.Println(err)
			os.Exit(1)
		}

		cmn.TrapSignal(func() {
			srv.Stop()
		})
	}

	return nil
}

//加载tendermint相关的配置
func loadTMConfig(ctx *cli.Context) *tmcfg.Config {
	tmHome := tendermintHomeFromEthermint(ctx)
	baseConfig := tmcfg.DefaultBaseConfig()
	baseConfig.RootDir = tmHome

	DefaultInstrumentationConfig := tmcfg.DefaultInstrumentationConfig()

	defaultTmConfig := tmcfg.DefaultConfig()
	defaultTmConfig.BaseConfig = baseConfig
	defaultTmConfig.Mempool.RootDir = tmHome
	defaultTmConfig.Mempool.Recheck = true //fix nonce bug
	defaultTmConfig.P2P.RootDir = tmHome
	defaultTmConfig.RPC.RootDir = tmHome
	defaultTmConfig.Consensus.RootDir = tmHome
	defaultTmConfig.Consensus.CreateEmptyBlocks = ctx.GlobalBool(emtUtils.TmConsEmptyBlock.Name)
	defaultTmConfig.Consensus.CreateEmptyBlocksInterval = time.Duration(ctx.GlobalInt(emtUtils.TmConsEBlockInteval.Name)) * time.Second
	defaultTmConfig.Consensus.NeedProofBlock = ctx.GlobalBool(emtUtils.TmConsNeedProofBlock.Name)

	defaultTmConfig.Instrumentation = DefaultInstrumentationConfig

	defaultTmConfig.FastSync = ctx.GlobalBool(emtUtils.FastSync.Name)
	defaultTmConfig.BaseConfig.InitialEthAccount = ctx.GlobalString(emtUtils.TmInitialEthAccount.Name)
	defaultTmConfig.PrivValidatorListenAddr = ctx.GlobalString(emtUtils.PrivValidatorListenAddr.Name)
	defaultTmConfig.PrivValidator = ctx.GlobalString(emtUtils.PrivValidator.Name)
	defaultTmConfig.P2P.AddrBook = ctx.GlobalString(emtUtils.AddrBook.Name)
	defaultTmConfig.P2P.AddrBookStrict = ctx.GlobalBool(emtUtils.RoutabilityStrict.Name)
	defaultTmConfig.P2P.PersistentPeers = ctx.GlobalString(emtUtils.PersistentPeers.Name)
	defaultTmConfig.P2P.PrivatePeerIDs = ctx.GlobalString(emtUtils.PrivatePeerIDs.Name)
	defaultTmConfig.P2P.ListenAddress = ctx.GlobalString(emtUtils.TendermintP2PListenAddress.Name)
	defaultTmConfig.P2P.ExternalAddress = ctx.GlobalString(emtUtils.TendermintP2PExternalAddress.Name)

	return defaultTmConfig
}

func configLoggerLevel(ctx *cli.Context, logger *tmlog.Logger) {
	switch ctx.GlobalString(emtUtils.LogLevelFlag.Name) {
	case "error":
		*logger = tmlog.NewFilter(*logger, tmlog.AllowError())
	case "info":
		*logger = tmlog.NewFilter(*logger, tmlog.AllowInfo())
	default:
		*logger = tmlog.NewFilter(*logger, tmlog.AllowAll())
	}
}

// nolint
// startNode copies the logic from go-ethereum
func startNode(ctx *cli.Context, stack *ethereum.Node) {
	emtUtils.StartNode(stack)

	// Unlock any account specifically requested
	ks := stack.AccountManager().Backends(keystore.KeyStoreType)[0].(*keystore.KeyStore)

	passwords := ethUtils.MakePasswordList(ctx)
	unlocks := strings.Split(ctx.GlobalString(ethUtils.UnlockedAccountFlag.Name), ",")
	for i, account := range unlocks {
		if trimmed := strings.TrimSpace(account); trimmed != "" {
			unlockAccount(ctx, ks, trimmed, i, passwords)
		}
	}
	// Register wallet event handlers to open and auto-derive wallets
	events := make(chan accounts.WalletEvent, 16)
	stack.AccountManager().Subscribe(events)

	go func() {
		// Create an chain state reader for self-derivation
		rpcClient, err := stack.Attach()
		if err != nil {
			ethUtils.Fatalf("Failed to attach to self: %v", err)
		}
		stateReader := ethclient.NewClient(rpcClient)

		// Open and self derive any wallets already attached
		for _, wallet := range stack.AccountManager().Wallets() {
			if err := wallet.Open(""); err != nil {
				log.Warn("Failed to open wallet", "url", wallet.URL(), "err", err)
			} else {
				wallet.SelfDerive(accounts.DefaultBaseDerivationPath, stateReader)
			}
		}
		// Listen for wallet event till termination
		for event := range events {
			switch event.Kind {
			case accounts.WalletArrived:
				if err := event.Wallet.Open(""); err != nil {
					log.Warn("New wallet appeared, failed to open", "url", event.Wallet.URL(), "err", err)
				}
			case accounts.WalletOpened:
				status, _ := event.Wallet.Status()
				log.Info("New wallet appeared", "url", event.Wallet.URL(), "status", status)

				if event.Wallet.URL().Scheme == "ledger" {
					event.Wallet.SelfDerive(accounts.DefaultLedgerBaseDerivationPath, stateReader)
				} else {
					event.Wallet.SelfDerive(accounts.DefaultBaseDerivationPath, stateReader)
				}

			case accounts.WalletDropped:
				log.Info("Old wallet dropped", "url", event.Wallet.URL())
				event.Wallet.Close()
			}
		}
	}()
}

// tries unlocking the specified account a few times.
// nolint: unparam
func unlockAccount(ctx *cli.Context, ks *keystore.KeyStore, address string, i int,
	passwords []string) (accounts.Account, string) {

	account, err := ethUtils.MakeAddress(ks, address)
	if err != nil {
		ethUtils.Fatalf("Could not list accounts: %v", err)
	}
	for trials := 0; trials < 3; trials++ {
		prompt := fmt.Sprintf("Unlocking account %s | Attempt %d/%d", address, trials+1, 3)
		password := getPassPhrase(prompt, false, i, passwords)
		err = ks.Unlock(account, password)
		if err == nil {
			log.Info("Unlocked account", "address", account.Address.Hex())
			return account, password
		}
		if err, ok := err.(*keystore.AmbiguousAddrError); ok {
			log.Info("Unlocked account", "address", account.Address.Hex())
			return ambiguousAddrRecovery(ks, err, password), password
		}
		if err != keystore.ErrDecrypt {
			// No need to prompt again if the error is not decryption-related.
			break
		}
	}
	// All trials expended to unlock account, bail out
	ethUtils.Fatalf("Failed to unlock account %s (%v)", address, err)

	return accounts.Account{}, ""
}

// getPassPhrase retrieves the passwor associated with an account, either fetched
// from a list of preloaded passphrases, or requested interactively from the user.
// nolint: unparam
func getPassPhrase(prompt string, confirmation bool, i int, passwords []string) string {
	// If a list of passwords was supplied, retrieve from them
	if len(passwords) > 0 {
		if i < len(passwords) {
			return passwords[i]
		}
		return passwords[len(passwords)-1]
	}
	// Otherwise prompt the user for the password
	if prompt != "" {
		fmt.Println(prompt)
	}
	password, err := console.Stdin.PromptPassword("Passphrase: ")
	if err != nil {
		ethUtils.Fatalf("Failed to read passphrase: %v", err)
	}
	if confirmation {
		confirm, err := console.Stdin.PromptPassword("Repeat passphrase: ")
		if err != nil {
			ethUtils.Fatalf("Failed to read passphrase confirmation: %v", err)
		}
		if password != confirm {
			ethUtils.Fatalf("Passphrases do not match")
		}
	}
	return password
}

func ambiguousAddrRecovery(ks *keystore.KeyStore, err *keystore.AmbiguousAddrError,
	auth string) accounts.Account {

	fmt.Printf("Multiple key files exist for address %x:\n", err.Addr)
	for _, a := range err.Matches {
		fmt.Println("  ", a.URL)
	}
	fmt.Println("Testing your passphrase against all of them...")
	var match *accounts.Account
	for _, a := range err.Matches {
		if err := ks.Unlock(a, auth); err == nil {
			match = &a
			break
		}
	}
	if match == nil {
		ethUtils.Fatalf("None of the listed files could be unlocked.")
	}
	fmt.Printf("Your passphrase unlocked %s\n", match.URL)
	fmt.Println("In order to avoid this warning, remove the following duplicate key files:")
	for _, a := range err.Matches {
		if a != *match {
			fmt.Println("  ", a.URL)
		}
	}
	return *match
}
