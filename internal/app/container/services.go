package container

import aiaccessapp "github.com/dujiao-next/internal/modules/aiaccess/application"

// initServices 按依赖顺序装配各阶段 Service。
func (c *Container) initServices() {
	c.AiAccessService = aiaccessapp.New(c.AiAccessRepo)
	c.AiRemoteService = aiaccessapp.NewRemote(c.AiRemoteRepo, c.AiAccessService)
	c.AiActionService = aiaccessapp.NewActions(c.AiActionRepo)
	c.AiOrderReviewService = aiaccessapp.NewOrderReviews(c.AiOrderReviewRepo)
	c.AiOrderCancellationService = aiaccessapp.NewOrderCancellations(c.AiOrderCancellationRepo)
	c.AiWalletRefundService = aiaccessapp.NewWalletRefunds(c.AiWalletRefundRepo)
	c.initPolicyAndSettingServices()
	c.loadRuntimeSettings()
	c.initIdentityAndCatalogServices()
	c.initApplicationServices()
	c.initIntegrationServices()
	c.wireServiceDependencies()
}
