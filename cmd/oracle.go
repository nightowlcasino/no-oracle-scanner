package cmd

import (
	"errors"
	"os"
	"os/signal"
	"syscall"

	"github.com/nats-io/nats.go"
	"github.com/nightowlcasino/nightowl/logger"
	"github.com/nightowlcasino/no-oracle-scanner/scanner"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	hostname     string
	natsEndpoint string
	cfgFile      string

	cmd = oracleScannerClientCommand()

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

	if value := viper.Get("HOSTNAME"); value != nil {
		hostname = value.(string)
	} else {
		var err error
		hostname, err = os.Hostname()
		if err != nil {
			logger.WithError(err).Infof(0, "unable to get hostname")
			os.Exit(1)
		}
	}

	logger.SetDefaults(logger.Fields{
		"app": "no-oracle-scanner",
		"host": hostname,
	})

	if cfgFile != "" {
		viper.SetConfigFile(cfgFile)
	} else {
		// use current directory
		dir, err := os.Getwd()
		if err != nil {
			logger.WithError(err).Infof(0, "failed to get working directory")
			os.Exit(1)
		}
		viper.AddConfigPath(dir)
		viper.SetConfigName("config")
		viper.SetConfigType("yaml")
	}

	err := viper.ReadInConfig()
	if err != nil {
		logger.WithError(err).Infof(0, "failed to read config file")
		os.Exit(1)
	}
	logger.Infof(0, "using config file: %s", viper.ConfigFileUsed())

	// set defaults
	viper.SetDefault("ergo_node.fqdn", "213.239.193.208")
	viper.SetDefault("ergo_node.scheme", "http")
	viper.SetDefault("ergo_node.port", 9053)
	viper.SetDefault("nats.random_number_subj", "drand.hash")
	viper.SetDefault("nightowl.test_mode", false)
}

func oracleScannerClientCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "no-oracle-scanner",
		Short: "Listens for drand random numbers and attaches them to erg nightowl utxo box id bets.",
		SilenceErrors: true,
		RunE: func(_ *cobra.Command, _ []string) error {

			if value := viper.Get("logging.level"); value != nil {
				lvl, err := logger.ParseLevel(value.(string))
				if err != nil {
					logger.Warnf(1, "config logging.level is not valid, defaulting to info log level")
					logger.SetVerbosity(1)
				}
				logger.SetVerbosity(lvl)
			} else {
				logger.Warnf(1, "config logging.level is not found, defaulting to info log level")
				logger.SetVerbosity(1)
			}

			// validate configs and set defaults if necessary
			if value := viper.Get("nats.endpoint"); value != nil {
				natsEndpoint = value.(string)
			} else {
				natsEndpoint = nats.DefaultURL
			}

			if value := viper.Get("ergo_node.api_key"); value == nil {
				logger.WithError(MissingNodeApiKeyErr).Infof(0, "required config is absent")
				return MissingNodeApiKeyErr
			}

			if value := viper.Get("ergo_node.password"); value == nil {
				logger.WithError(MissingNodePasswordErr).Infof(0, "required config is absent")
				return MissingNodePasswordErr
			}

			if value := viper.Get("ergo_node.wallet_password"); value == nil {
				logger.WithError(MissingNodeWalletPassErr).Infof(0, "required config is absent")
				return MissingNodeWalletPassErr
			}

			// Connect to the nats server
			nats, err := nats.Connect(natsEndpoint)
			if err != nil {
				logger.WithError(err).Infof(0, "failed to connect to %s nats server", natsEndpoint)
				return err
			}

			svc, err := scanner.NewService(nats)
			if err != nil {
				logger.WithError(err).Infof(0, "failed to create no-oracle-scanner service")
				return err
			}

			svc.Start()

			signals := make(chan os.Signal, 1)
			signal.Notify(signals, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
			go func() {
				s := <-signals
				logger.Infof(0, "%s signal caught, stopping app", s.String())
				svc.Stop()
			}()

			logger.Infof(0, "service started...")

			svc.Wait()

			return nil
		},
	}
}