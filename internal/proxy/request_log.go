package proxy

import (
	"time"

	"clisimplehub/internal/executor"
)

// RequestDetail holds extended request information for detail view
type RequestDetail struct {
	Method         string
	StatusCode     int
	TargetURL      string
	RequestHeaders map[string]string
	RequestStream  string
	ResponseStream string
	UpstreamAuth   string
	Model          string
}

func (p *ProxyServer) recordRequestWithDetail(id string, interfaceType InterfaceType, transport string, endpoint *executor.EndpointConfig, path string, startTime time.Time, status string, runTime int64, detail *RequestDetail) {
	log := &RequestLog{
		ID:            id,
		InterfaceType: string(interfaceType),
		Transport:     transport,
		Path:          path,
		RunTime:       runTime,
		Status:        status,
		Timestamp:     startTime,
	}

	if endpoint != nil {
		log.EndpointName = endpoint.Name
		log.ProviderName = endpoint.ProviderName
		log.Transformer = endpoint.Transformer
	}

	if detail != nil {
		log.Method = detail.Method
		log.StatusCode = detail.StatusCode
		log.TargetURL = detail.TargetURL
		log.RequestHeaders = detail.RequestHeaders
		log.RequestStream = detail.RequestStream
		log.ResponseStream = detail.ResponseStream
		log.UpstreamAuth = detail.UpstreamAuth
		log.Model = detail.Model
	}

	p.stats.RecordRequest(log)
}
