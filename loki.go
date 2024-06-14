package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"gopkg.in/mcuadros/go-syslog.v2/format"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"time"
)

const (
	pushApi       = "/loki/api/v1/push"
	readyApi      = "/ready"
	logChanLen    = 10240
	maxStreamLine = 1024 * 4
)

type lokiClient struct {
	config config
	//instance     string
	ready        bool
	logChan      chan lokiStream
	ctx          context.Context
	cancelFunc   context.CancelFunc
	flushStreams lokiPushStreams
}

type lokiPushStreams struct {
	Streams []lokiStream `json:"streams"`
}

type lokiStream struct {
	Stream map[string]interface{} `json:"stream"`
	Values [][2]string            `json:"values"`
}

type requestConfig struct {
	Url        string
	Headers    map[string]string
	Parameters map[string]string
	Body       io.Reader
}

func newLokiClient(cfg config) (*lokiClient, error) {
	if _, e := url.Parse(cfg.LokiServer); e != nil {
		return nil, errors.New("Invalid format of LokiServer" + e.Error())
	}
	var rt lokiClient
	rt.config = cfg
	rt.setLokiReadyStatus()
	rt.ctx, rt.cancelFunc = context.WithCancel(context.Background())
	rt.logChan = make(chan lokiStream, logChanLen)
	go rt.run()
	return &rt, nil
}

func (k *lokiClient) getLabels(tag string) (map[string]interface{}, bool) {
	for _, s := range k.config.ScrapeConfig {
		if s.Tag == tag {
			return s.Labels, true
		}
	}
	return nil, false
}

// Push 将日志写入缓存
func (k *lokiClient) Push(log format.LogParts) {
	var ls lokiStream
	ls.Stream = make(map[string]interface{})
	var tag string
	//If a tag is defined in the syslog, use the tag to search for config.logLabel.
	if v, ok := log["tag"]; ok && v != "" {
		if labelsByTag, tagOk := k.getLabels(fmt.Sprint(v)); tagOk {
			tag = fmt.Sprint(v)
			ls.Stream = labelsByTag
		} else { //否则使用IP搜索
			client := strings.Split(fmt.Sprint(log["client"]), ":")
			if len(client) == 2 {
				tag = client[0]
				if labelsByIp, ipOk := k.getLabels(tag); ipOk {
					ls.Stream = labelsByIp
				}
			}
		}
	}
	ls.Stream["tag"] = tag
	if severity, ok := log["severity"]; ok {
		ls.Stream["severity"] = severity
	}
	if facility, ok := log["facility"]; ok {
		ls.Stream["facility"] = facility
	}
	if hostname, ok := log["hostname"]; ok {
		ls.Stream["hostname"] = hostname
	}
	if priority, ok := log["priority"]; ok {
		ls.Stream["priority"] = priority
	}
	//By default, the current time is used as the timestamp.
	ts := time.Now()
	if timestamp, ok := log["timestamp"]; ok {
		ts, _ = time.ParseInLocation("2006-01-02 15:04:05 +0000 UTC", fmt.Sprint(timestamp), time.Local)
	}
	ls.Values = append(ls.Values, [2]string{
		fmt.Sprint(ts.UnixNano()),
		k.getMessage("%s", []interface{}{log["content"]}),
	})
	k.logChan <- ls
}

func (k *lokiClient) Close() {
	k.cancelFunc()
}

func (k *lokiClient) run() {
	ticker := time.NewTicker(time.Second * time.Duration(3))
	readyTicker := time.NewTicker(time.Second * time.Duration(10))
waitLoop:
	for {
		select {
		case <-ticker.C:
			k.flush()
		case stream := <-k.logChan:
			k.flushStreams.Streams = append(k.flushStreams.Streams, stream)
		case <-readyTicker.C:
			k.setLokiReadyStatus()
		case <-k.ctx.Done():
			k.flush()
			slog.Info("[LokiClient]" + "Exiting loki client.")
			break waitLoop
		}
	}
}

func (k *lokiClient) flush() {
	if len(k.flushStreams.Streams) == 0 {
		return
	}
	if k.ready == false {
		slog.Warn("[LokiClient]" + "loki is not ready")
		return
	}
	orderLogs := k.splitStreams()
	for _, orderLog := range orderLogs {
		if b, e := json.Marshal(orderLog); e != nil {
			slog.Warn("[LokiClient]" + "json.Marshal lokiPushStreams fail:" + e.Error())
			return
		} else {
			var reqCfg requestConfig
			reqCfg.Url = k.config.LokiServer + pushApi
			reqCfg.Headers = make(map[string]string)
			reqCfg.Headers["Content-Type"] = "application/json"
			reqCfg.Body = bytes.NewReader(b)
			if _, _, eh := k.sendHttpRequest("POST", &reqCfg); eh != nil {
				slog.Warn("[LokiClient]" + "SendHttpRequest fail:" + eh.Error())
				return
			} else {
				var items, maxCnt int
				for _, v := range orderLog.Streams {
					items += len(v.Values)
					if maxCnt < len(v.Values) {
						maxCnt = len(v.Values)
					}
				}
				slog.Debug("[LokiClient]" + "Push log to LokiServer success:" + fmt.Sprint(items) + " item(s) in " + fmt.Sprint(len(orderLog.Streams)) + " stream(s),max items of stream =" + fmt.Sprint(maxCnt))

			}
		}
	}
	//Clear cache
	k.flushStreams.Streams = k.flushStreams.Streams[0:0]
	return
}

// Split the streams in the cache into multiple streams of fixed size.
func (k *lokiClient) splitStreams() []lokiPushStreams {
	var rt []lokiPushStreams
	batch := len(k.flushStreams.Streams)/maxStreamLine + 1
	for i := 0; i < batch; i++ {
		if i < batch-1 {
			rt = append(rt, k.makeOrder(k.flushStreams.Streams[i*maxStreamLine:(i+1)*maxStreamLine]))
		} else {
			rt = append(rt, k.makeOrder(k.flushStreams.Streams[i*maxStreamLine:len(k.flushStreams.Streams)]))
		}
	}
	return rt
}

// Put syslog with the same label into the same stream to improve Loki's ingestion performance.
func (k *lokiClient) makeOrder(logStreams []lokiStream) lokiPushStreams {
	var rt lokiPushStreams
	var find bool
	for k1, v1 := range logStreams {
		find = false
		for k2, v2 := range rt.Streams {
			//找到同样的Label
			if reflect.DeepEqual(v1.Stream, v2.Stream) {
				find = true
				rt.Streams[k2].Values = append(rt.Streams[k2].Values, logStreams[k1].Values...)
				break
			}
		}
		if find == false {
			var s lokiStream
			s.Stream = v1.Stream
			s.Values = v1.Values
			rt.Streams = append(rt.Streams, s)
		}
	}
	return rt
}

func (k *lokiClient) getMessage(template string, fmtArgs []interface{}) string {
	if len(fmtArgs) == 0 {
		return template
	}

	if template != "" {
		return fmt.Sprintf(template, fmtArgs...)
	}

	if len(fmtArgs) == 1 {
		if str, ok := fmtArgs[0].(string); ok {
			return str
		}
	}
	return fmt.Sprint(fmtArgs...)
}

func (k *lokiClient) sendHttpRequest(httpMethod string, reqCfg *requestConfig) ([]byte, int, error) {
	if request, errh := http.NewRequest(httpMethod, reqCfg.Url, reqCfg.Body); errh == nil {
		q := request.URL.Query()
		for k1, v := range reqCfg.Parameters {
			q.Add(k1, v)
		}
		request.URL.RawQuery = q.Encode()
		for k1, v := range reqCfg.Headers {
			request.Header.Set(k1, v)
		}
		//request.Header.Set("Content-Encoding", "gzip")
		client := http.Client{Timeout: time.Second * time.Duration(3)}
		if httpResp, errc := client.Do(request); errc == nil {
			defer httpResp.Body.Close()
			body, _ := io.ReadAll(httpResp.Body)
			return body, httpResp.StatusCode, nil
		} else {
			return nil, 0, errc
		}
	} else {
		return nil, 0, errh
	}
}

func (k *lokiClient) setLokiReadyStatus() {
	if u, e := url.Parse(k.config.LokiServer + readyApi); e != nil {
		slog.Warn("[LokiClient]" + "Loki Server is not ready,LokiServer " + k.config.LokiServer + " format(like http://loki:3100) is wrong.")
		k.ready = false
		return
	} else if u.Scheme != "http" && u.Scheme != "https" {
		slog.Warn("[LokiClient]" + "Loki Server is not ready,LokiServer " + k.config.LokiServer + " format(like http(https)://loki:3100) is wrong.")
		k.ready = false
		return
	}
	var reqCfg requestConfig
	reqCfg.Url = k.config.LokiServer + readyApi
	//retry 3 times
	for i := 0; i < 3; i++ {
		if _, status, eh := k.sendHttpRequest("GET", &reqCfg); eh == nil && status == http.StatusOK {
			k.ready = true
			return
		} else {
			slog.Warn("[LokiClient]" + "Loki Server " + k.config.LokiServer + " is not ready,err=" + eh.Error() + " http status=" + fmt.Sprint(status))
		}
	}
	k.ready = false
}
