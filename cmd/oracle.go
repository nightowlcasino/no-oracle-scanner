package cmd

import (
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/nats-io/nats.go"
	"github.com/nightowlcasino/nightowl/logger"
	"github.com/nightowlcasino/no-oracle-scanner/controller"
	"github.com/nightowlcasino/no-oracle-scanner/scanner"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"go.uber.org/zap"
)

var (
	hostname     string
	natsEndpoint string
	cfgFile      string

	cmd = oracleScannerClientCommand()

	log *zap.Logger

	MissingNodePasswordErr = errors.New("config ergo_node.password is missing")
	MissingNodeWalletPassErr = errors.New("config ergo_node.wallet_password is missing")
	MissingNodeApiKeyErr = errors.New("config ergo_node.api_key is missing")
)

// Execute start the no-oracle-scanner app
func Execute() error {
	return cmd.Execute()
}

func init() {
	cobra.OnInitialize(initConfig)

	cmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default is ./config.yml)")
	viper.BindPFlag("config",cmd.Flags().Lookup("config"))
	viper.BindEnv("HOSTNAME")
}

func initConfig() {

	logger.Initialize("no-oracle-scanner")

	if value := viper.Get("HOSTNAME"); value != nil {
		hostname = value.(string)
	} else {
		var err error
		hostname, err = os.Hostname()
		if err != nil {
			fmt.Printf("unable to get hostname - %s", err.Error())
			os.Exit(1)
		}
	}

	log = zap.L()
	defer log.Sync()

	if cfgFile != "" {
		viper.SetConfigFile(cfgFile)
	} else {
		// use current directory
		dir, err := os.Getwd()
		if err != nil {
			log.Error("failed to get working directory", zap.Error(err))
			os.Exit(1)
		}
		viper.AddConfigPath(dir)
		viper.SetConfigName("config")
		viper.SetConfigType("yaml")
	}

	err := viper.ReadInConfig()
	if err != nil {
		log.Error("failed to read config file", zap.Error(err))
		os.Exit(1)
	}
	log.Info("successfully parsed config file", zap.String("file", viper.ConfigFileUsed()))

	// set defaults
	viper.SetDefault("ergo_node.fqdn", "213.239.193.208")
	viper.SetDefault("ergo_node.scheme", "http")
	viper.SetDefault("ergo_node.port", 9053)
	viper.SetDefault("nats.random_number_subj", "drand.hash")
	viper.SetDefault("nightowl.test_mode", false)
	viper.SetDefault("metrics.port", 8090)
}

func oracleScannerClientCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "no-oracle-scanner",
		Short: "Listens for drand random numbers and attaches them to erg nightowl utxo box id bets.",
		SilenceErrors: true,
		RunE: func(_ *cobra.Command, _ []string) error {

			//if value := viper.Get("logging.level"); value != nil {
			//	lvl, err := logger.ParseLevel(value.(string))
			//	if err != nil {
			//		logger.Warnf(1, "config logging.level is not valid, defaulting to info log level")
			//		logger.SetVerbosity(1)
			//	}
			//	logger.SetVerbosity(lvl)
			//} else {
			//	logger.Warnf(1, "config logging.level is not found, defaulting to info log level")
			//	logger.SetVerbosity(1)
			//}

			// validate configs and set defaults if necessary
			if value := viper.Get("nats.endpoint"); value != nil {
				natsEndpoint = value.(string)
			} else {
				natsEndpoint = nats.DefaultURL
			}

			if value := viper.Get("ergo_node.api_key"); value == nil {
				log.Error("required config is absent", zap.Error(MissingNodeApiKeyErr))
				return MissingNodeApiKeyErr
			}

			if value := viper.Get("ergo_node.password"); value == nil {
				log.Error("required config is absent", zap.Error(MissingNodePasswordErr))
				return MissingNodePasswordErr
			}

			if value := viper.Get("ergo_node.wallet_password"); value == nil {
				log.Error("required config is absent", zap.Error(MissingNodeWalletPassErr))
				return MissingNodeWalletPassErr
			}

			// Connect to the nats server
			nats, err := nats.Connect(natsEndpoint)
			if err != nil {
				log.Error("failed to connect to nats server", zap.Error(err), zap.String("endpoint", natsEndpoint))
				return err
			}

			svc, err := scanner.NewService(nats)
			if err != nil {
				log.Error("failed to create no-oracle-scanner service", zap.Error(err), zap.String("endpoint", natsEndpoint))
				return err
			}

			svc.Start()

			// metrics server
			router := controller.NewRouter()
			server := controller.NewServer(router)
			server.Start()

			signals := make(chan os.Signal, 1)
			signal.Notify(signals, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
			go func() {
				s := <-signals
				log.Info(s.String() + " signal caught, stopping app")
				svc.Stop()
				server.Stop()
			}()

			log.Info("service started...")

			svc.Wait()
			server.Wait()

			return nil
		},
	}
}