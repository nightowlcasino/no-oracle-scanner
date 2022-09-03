package controller

import (
	"encoding/json"
	"net/http"

	"github.com/julienschmidt/httprouter"
	"github.com/nightowlcasino/nightowl/buildinfo"
)

type Router struct {
	http.Handler

	ready bool
}

func (r *Router) Ready() {
	r.ready = true
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

	r.ready = true

	return r
}