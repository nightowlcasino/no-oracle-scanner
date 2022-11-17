package controller

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nightowlcasino/no-oracle-scanner/scanner"
)

func TestUnsignedTransaction(t *testing.T) {
	
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/transactions/unsigned", nil)

	GetUnsignedTxs()(w, req, nil)
	res := w.Result()
	data, err := io.ReadAll(res.Body)
	if err != nil {
		t.Errorf("expected error to be nil got %v", err)
	}

	if string(data) != "{}\n" {
		t.Errorf("expected {} got %v", string(data))
	}
	res.Body.Close()

	scanner.TxRetryer.Add("1234", []byte("Test payload"))
	GetUnsignedTxs()(w, req, nil)
	res = w.Result()
	data, err = io.ReadAll(res.Body)
	if err != nil {
		t.Errorf("expected error to be nil got %v", err)
	}

	if string(data) == "{}\n" {
		t.Errorf("expected some data got %v", string(data))
	}
	res.Body.Close()
}

func TestDeleteUnsignedTransaction(t *testing.T) {
	
	w := httptest.NewRecorder()
	getReq := httptest.NewRequest(http.MethodGet, "/transactions/unsigned", nil)
	deleteReq := httptest.NewRequest(http.MethodDelete, "/transactions/unsigned?uuid=1234", nil)

	scanner.TxRetryer.Add("1234", []byte("Test payload"))
	DeleteUnsignedTxs()(w, deleteReq, nil)
	res := w.Result()
	data, err := io.ReadAll(res.Body)
	if err != nil {
		t.Errorf("expected error to be nil got %v", err)
	}

	if string(data) != "" {
		t.Errorf("expected \"\" got %v", string(data))
	}
	res.Body.Close()

	GetUnsignedTxs()(w, getReq, nil)
	res = w.Result()
	data, err = io.ReadAll(res.Body)
	if err != nil {
		t.Errorf("expected error to be nil got %v", err)
	}

	t.Logf("%v\n", string(data))
	t.Logf("%v\n", data)
	if string(data) != "" {
		t.Errorf("expected \"\" data got %v", string(data))
	}
	res.Body.Close()
}

func TestAddDeleteAddUnsignedTransaction(t *testing.T) {
	
	w1 := httptest.NewRecorder()
	w2 := httptest.NewRecorder()
	getReq := httptest.NewRequest(http.MethodGet, "/transactions/unsigned", nil)
	deleteReq := httptest.NewRequest(http.MethodDelete, "/transactions/unsigned?uuid=1234", nil)

	scanner.TxRetryer.Add("1234", []byte("Test payload"))
	DeleteUnsignedTxs()(w1, deleteReq, nil)
	res := w1.Result()
	data, err := io.ReadAll(res.Body)
	if err != nil {
		t.Errorf("expected error to be nil got %v", err)
	}

	if string(data) != "" {
		t.Errorf("expected \"\" got %v", string(data))
	}
	res.Body.Close()

	scanner.TxRetryer.Add("5678", []byte("Test payload"))

	GetUnsignedTxs()(w2, getReq, nil)
	res = w2.Result()
	data, err = io.ReadAll(res.Body)
	if err != nil {
		t.Errorf("expected error to be nil got %v", err)
	}

	if string(data) == "" {
		t.Errorf("expected some data data got %v", string(data))
	}
	res.Body.Close()
}