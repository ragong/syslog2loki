# syslog2Loki
`syslog2Loki` is designed to listen for syslog logs and, based on custom labels, write them into a Grafana Loki log server.

## Compatibility
RFC3164/RFC5424/RFC6587  
Different protocol versions may have different log labels.  

## Usage

### Config
`syslog2Loki` uses a configuration file in JSON format. For example:
```json lines
{
  "SyslogBind": "0.0.0.0:514",
  "LokiServer": "http://10.227.11.61:13100",
  "ScrapeConfig": [
    {
      "Tag": "nginxLog",
      "Labels": {
        "AppName":"Nginx",
        "ServerName": "6.175",
        "OS":"Centos",
        "NgxVersion": "nginx/1.22.1"
      }
    },
    {
      "Tag": "192.168.6.175",
      "Labels": {
        "AppName":"NginxWithIpTag",
        "ServerName": "6.175",
        "OS":"Centos",
        "NgxVersion": "nginx/1.22.1"
      }
    }
  ]
}
```
Explanation:
```text
SyslogBind:Syslog service listen port.
LokiServer:Grafana loki server url.
ScrapeConfig:
Tag:Syslog tag.You can define or use the sender's IP when sending a syslog. If nothing is defined, the sender's IP will be used as the default tag.
Labels:Custom key-value pairs are used to distinguish different services of the syslog sender.
```

### Build
```shell
go build .
```

### Run
```shell
Usage of syslog2loki
  -c string
        path of config.json (default "./config.json")
```