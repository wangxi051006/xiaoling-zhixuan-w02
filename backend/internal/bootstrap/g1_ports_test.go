package bootstrap

import (
	"campus.local/xiaoling/internal/catalog"
	"campus.local/xiaoling/internal/contract"
	"campus.local/xiaoling/internal/identity"
	"campus.local/xiaoling/internal/schedule"
	"campus.local/xiaoling/internal/settlement"
)

var (
	_ contract.IdentityPort   = (*identity.Directory)(nil)
	_ contract.CatalogPort    = catalog.Store{}
	_ contract.SchedulePort   = schedule.Store{}
	_ contract.SettlementPort = settlement.Store{}
)
