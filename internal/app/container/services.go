package container

import aiaccessapp "github.com/dujiao-next/internal/modules/aiaccess/application"

// initServices 按依赖顺序装配各阶段 Service。
func (c *Container) initServices() {
	c.AiAccessService = aiaccessapp.New(c.AiAccessRepo)
	c.initPolicyAndSettingServices()
	c.loadRuntimeSettings()
	c.initIdentityAndCatalogServices()
	c.initApplicationServices()
	c.initIntegrationServices()
	c.wireServiceDependencies()
}
