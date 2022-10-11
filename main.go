package main

import (
	"github.com/nightowlcasino/no-oracle-scanner/cmd"
	"go.uber.org/zap"
)

var (
	log *zap.Logger
)

func main() {

	if err := cmd.Execute(); err != nil {
		log = zap.L()
		log.Error("failed to execute no-oracle-scanner", zap.Error(err))
	}
}