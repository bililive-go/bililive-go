package servers

import (
	"encoding/json"
	"net/http"
)

const (
	contentType     = "Content-Type"
	contentTypeJSON = "application/json"
)

func writeMsg(w http.ResponseWriter, code int, msg string) {
	w.WriteHeader(code)
	_, _ = w.Write([]byte(msg))
}

func writeJSON(w http.ResponseWriter, obj any) {
	b, err := json.Marshal(obj)
	if err != nil {
		writeMsg(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set(contentType, contentTypeJSON)
	_, _ = w.Write(b)
}

func writeJsonWithStatusCode(w http.ResponseWriter, code int, obj any) {
	b, err := json.Marshal(obj)
	if err != nil {
		writeMsg(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(code)
	w.Header().Set(contentType, contentTypeJSON)
	_, _ = w.Write(b)
}
