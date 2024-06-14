package main

type config struct {
	SyslogBind   string     `json:"SyslogBind"`
	LokiServer   string     `json:"LokiServer"`
	ScrapeConfig []logLabel `json:"ScrapeConfig"`
}

type logLabel struct {
	Tag    string                 `json:"Tag"`
	Labels map[string]interface{} `json:"Labels"`
}
