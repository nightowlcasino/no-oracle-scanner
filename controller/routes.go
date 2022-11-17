package controller

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"github.com/julienschmidt/httprouter"
	"github.com/nightowlcasino/nightowl/buildinfo"
	"github.com/nightowlcasino/no-oracle-scanner/scanner"
)

type Router struct {
	http.Handler

	ready bool
}

func (r *Router) Ready() {
	r.ready = true
}

func GetUnsignedTxs() httprouter.Handle {
	return func(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
		json.NewEncoder(w).Encode(scanner.TxRetryer.UnsignedTxs)
	}
}

func DeleteUnsignedTxs() httprouter.Handle {
	return func(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
		query, err := url.ParseQuery(req.URL.RawQuery)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, "invalid url request")
			return
		}
		uuid := query.Get("uuid")
		if uuid == "" {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, "missing uuid url parameter")
			return
		}
		scanner.TxRetryer.Delete(uuid)
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "")
	}
}

func NewRouter() *Router {
	h := httprouter.New()
	h.RedirectTrailingSlash = false
	h.RedirectFixedPath = false

	r := &Router{
		Handler: h,
	}

	h.GET("/info", func(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
		json.NewEncoder(w).Encode(buildinfo.Info)
	})
	h.GET("/transactions/unsigned", GetUnsignedTxs())
	h.DELETE("/transactions/unsigned", DeleteUnsignedTxs())

	r.ready = true

	return r
}