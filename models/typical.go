package models

// Result typical result answer
type Result struct {
	Result interface{} `json:"result"`
	Error  interface{} `json:"error"`
}
