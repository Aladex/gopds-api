package models

type Result struct {
	Result interface{} `json:"result"`
	Error  interface{} `json:"error"`
}
