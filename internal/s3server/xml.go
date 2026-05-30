package s3server

import (
	"encoding/xml"
	"net/http"
)

func xmlResponse(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(xml.Header))
	enc := xml.NewEncoder(w)
	_ = enc.Encode(v)
}

func xmlError(w http.ResponseWriter, status int, code, message, resource string) {
	xmlResponse(w, status, Error{
		Code:     code,
		Message:  message,
		Resource: resource,
	})
}
