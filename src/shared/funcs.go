package shared

import (
	"encoding/json"
	"net/http"
)

func SendError(w http.ResponseWriter, code int, message string) {
	json.NewEncoder(w).Encode(&CustomHttpError{Code: code, Message: message})
}
