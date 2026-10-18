package shared

type CustomHttpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}
