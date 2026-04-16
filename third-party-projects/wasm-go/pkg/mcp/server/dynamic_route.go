// Copyright (c) 2022 Alibaba Group Holding Ltd.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package server

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/higress-group/proxy-wasm-go-sdk/proxywasm"
	"github.com/higress-group/wasm-go/pkg/log"
	"github.com/higress-group/wasm-go/pkg/mcp/utils"
	"github.com/higress-group/wasm-go/pkg/wrapper"
)

const DestinationHeader = "x-higress-destination"

func routeOrHttpCall(
	ctx wrapper.HttpContext,
	method, rawURL string,
	headers [][2]string,
	body []byte,
	callback func(int, [][2]string, []byte),
) error {
	destination, filteredHeaders := stripDestinationHeader(headers)
	if destination == "" {
		return ctx.RouteCall(method, rawURL, headers, body, callback)
	}

	targetCluster, ok := parseDestinationToTargetCluster(destination)
	if !ok {
		log.Warnf("invalid %s header value %q, fallback to RouteCall", DestinationHeader, destination)
		return ctx.RouteCall(method, rawURL, filteredHeaders, body, callback)
	}

	currentCluster, err := getCurrentClusterName()
	if err == nil && currentCluster == targetCluster.ClusterName() {
		return ctx.RouteCall(method, rawURL, filteredHeaders, body, callback)
	}
	if err != nil {
		log.Warnf("failed to get current cluster name, fallback to HttpCall for %q: %v", destination, err)
	}

	finalHeaders := mergeInheritedHeadersForHttpCall(copyHeadersForDynamicHttpCall(), filteredHeaders)

	ctx.SetContext(utils.CtxNeedPause, true)
	return wrapper.HttpCall(targetCluster, method, rawURL, finalHeaders, body, func(statusCode int, responseHeaders http.Header, responseBody []byte) {
		callback(statusCode, convertHTTPHeaderToPairs(responseHeaders), responseBody)
	})
}

func stripDestinationHeader(headers [][2]string) (string, [][2]string) {
	filteredHeaders := make([][2]string, 0, len(headers))
	var destination string
	for _, kv := range headers {
		if strings.EqualFold(kv[0], DestinationHeader) {
			destination = strings.TrimSpace(kv[1])
			continue
		}
		filteredHeaders = append(filteredHeaders, kv)
	}
	return destination, filteredHeaders
}

func copyHeadersForDynamicHttpCall() [][2]string {
	headers := make([][2]string, 0)
	skipHeaders := map[string]bool{
		"content-length":                   true,
		"transfer-encoding":                true,
		":path":                            true,
		":method":                          true,
		":scheme":                          true,
		":authority":                       true,
		strings.ToLower(DestinationHeader): true,
	}

	headerMap, err := proxywasm.GetHttpRequestHeaders()
	if err != nil {
		log.Warnf("failed to get request headers for dynamic HttpCall: %v", err)
		return [][2]string{}
	}

	for _, header := range headerMap {
		headerName := strings.ToLower(header[0])
		if skipHeaders[headerName] {
			continue
		}
		headers = append(headers, header)
	}

	return headers
}

func mergeInheritedHeadersForHttpCall(baseHeaders, overrideHeaders [][2]string) [][2]string {
	merged := make([][2]string, len(baseHeaders))
	copy(merged, baseHeaders)
	for _, header := range overrideHeaders {
		setOrReplaceHeader(&merged, header[0], header[1])
	}
	return merged
}

func parseDestinationToTargetCluster(destination string) (wrapper.TargetCluster, bool) {
	destination = strings.TrimSpace(destination)
	separator := strings.LastIndex(destination, ":")
	if separator <= 0 || separator == len(destination)-1 {
		return wrapper.TargetCluster{}, false
	}

	host := strings.TrimSpace(destination[:separator])
	port := strings.TrimSpace(destination[separator+1:])
	if host == "" || port == "" {
		return wrapper.TargetCluster{}, false
	}
	if _, err := strconv.Atoi(port); err != nil {
		return wrapper.TargetCluster{}, false
	}

	return wrapper.TargetCluster{
		Host:    host,
		Cluster: fmt.Sprintf("outbound|%s||%s", port, host),
	}, true
}

func getCurrentClusterName() (string, error) {
	clusterName, err := proxywasm.GetProperty([]string{"cluster_name"})
	if err != nil {
		return "", err
	}
	return string(clusterName), nil
}

func convertHTTPHeaderToPairs(headers http.Header) [][2]string {
	pairs := make([][2]string, 0, len(headers))
	for key, values := range headers {
		for _, value := range values {
			pairs = append(pairs, [2]string{key, value})
		}
	}
	return pairs
}
