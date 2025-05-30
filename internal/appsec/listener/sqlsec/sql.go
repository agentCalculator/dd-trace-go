// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package sqlsec

import (
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/dyngo"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/emitter/sqlsec"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/emitter/waf/addresses"
	"github.com/DataDog/dd-trace-go/v2/internal/appsec/config"
	"github.com/DataDog/dd-trace-go/v2/internal/appsec/emitter/waf"
	"github.com/DataDog/dd-trace-go/v2/internal/appsec/listener"
)

type Feature struct{}

func (*Feature) String() string {
	return "SQLi Protection"
}

func (*Feature) Stop() {}

func NewSQLSecFeature(cfg *config.Config, rootOp dyngo.Operation) (listener.Feature, error) {
	if !cfg.RASP || !cfg.SupportedAddresses.AnyOf(addresses.ServerDBTypeAddr, addresses.ServerDBStatementAddr) {
		return nil, nil
	}

	feature := &Feature{}
	dyngo.On(rootOp, feature.OnStart)
	return feature, nil
}

func (*Feature) OnStart(op *sqlsec.SQLOperation, args sqlsec.SQLOperationArgs) {
	dyngo.EmitData(op, waf.RunEvent{
		Operation: op,
		RunAddressData: addresses.NewAddressesBuilder().
			WithDBStatement(args.Query).
			WithDBType(args.Driver).
			Build(),
	})
}
