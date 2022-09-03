package controller

import (
	"net/http"
	"strconv"
	"time"

	http_no "github.com/nightowlcasino/nightowl/http"
	"github.com/spf13/viper"
)

func NewServer(handler http.Handler) *http_no.Server {
	return http_no.NewServer(":"+strconv.Itoa(viper.Get("metrics.port").(int)),
		handler,
		http_no.ReadTimeout(1*time.Minute),
		http_no.WriteTimeout(1*time.Minute),
		http_no.IdleTimeout(2*time.Minute))
}