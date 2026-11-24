package helpers

import "net/http"

// errorBody is the single JSON envelope every error response uses.
type errorBody struct {
	Error errorDetail `json:"error"`
}

// errorDetail is the machine-readable code and human-readable message inside errorBody.
type errorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Error writes a JSON error envelope with the given status, machine code and message.
func Error(w http.ResponseWriter, status int, code, msg string) {
	JSON(w, status, errorBody{Error: errorDetail{Code: code, Message: msg}})
}
