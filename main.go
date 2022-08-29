package main

import (
	"github.com/nightowlcasino/nightowl/logger"
	"github.com/nightowlcasino/no-oracle-scanner/cmd"
)

func main() {

	if err := cmd.Execute(); err != nil {
		logger.WithError(err).Infof(0, "failed to execute no-oracle-scanner")
	}
}