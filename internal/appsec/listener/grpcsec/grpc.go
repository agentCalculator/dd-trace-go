// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package grpcsec

import (
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/dyngo"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/emitter/grpcsec"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/emitter/waf/addresses"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/trace"
	"github.com/DataDog/dd-trace-go/v2/internal/appsec/config"
	"github.com/DataDog/dd-trace-go/v2/internal/appsec/listener"
	"github.com/DataDog/dd-trace-go/v2/internal/appsec/listener/httpsec"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
)

type Feature struct{}

func (*Feature) String() string {
	return "gRPC Security"
}

func (*Feature) Stop() {}

func NewGRPCSecFeature(config *config.Config, rootOp dyngo.Operation) (listener.Feature, error) {
	if !config.SupportedAddresses.AnyOf(
		addresses.ClientIPAddr,
		addresses.GRPCServerMethodAddr,
		addresses.GRPCServerRequestMessageAddr,
		addresses.GRPCServerRequestMetadataAddr,
		addresses.GRPCServerResponseMessageAddr,
		addresses.GRPCServerResponseMetadataHeadersAddr,
		addresses.GRPCServerResponseMetadataTrailersAddr,
		addresses.GRPCServerResponseStatusCodeAddr) {
		return nil, nil
	}

	feature := &Feature{}
	dyngo.On(rootOp, feature.OnStart)
	dyngo.OnFinish(rootOp, feature.OnFinish)
	return feature, nil
}

func (f *Feature) OnStart(op *grpcsec.HandlerOperation, args grpcsec.HandlerOperationArgs) {
	ipTags, clientIP := httpsec.ClientIPTags(args.Metadata, false, args.RemoteAddr)
	log.Debug("appsec: http client ip detection returned `%s`", clientIP)

	op.SetStringTags(ipTags)

	SetRequestMetadataTags(op, args.Metadata)

	op.Run(op,
		addresses.NewAddressesBuilder().
			WithGRPCMethod(args.Method).
			WithGRPCRequestMetadata(args.Metadata).
			WithClientIP(clientIP).
			Build(),
	)
}

func (f *Feature) OnFinish(op *grpcsec.HandlerOperation, res grpcsec.HandlerOperationRes) {
	op.Run(op,
		addresses.NewAddressesBuilder().
			WithGRPCResponseStatusCode(res.StatusCode).
			Build(),
	)
}

func SetRequestMetadataTags(span trace.TagSetter, metadata map[string][]string) {
	for h, v := range httpsec.NormalizeHTTPHeaders(metadata) {
		span.SetTag("grpc.metadata."+h, v)
	}
}
