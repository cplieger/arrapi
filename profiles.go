package arrapi

import "context"

// GetQualityProfiles returns every quality profile defined on the instance.
func (c *client) GetQualityProfiles(ctx context.Context) ([]QualityProfile, error) {
	return doSingleflight(ctx, c, "qualityprofile", func(fctx context.Context) ([]QualityProfile, error) {
		return fetchAll[QualityProfile](fctx, c, apiPrefix+"/qualityprofile")
	})
}

// GetRootFolders returns every root folder configured on the instance.
func (c *client) GetRootFolders(ctx context.Context) ([]RootFolder, error) {
	return doSingleflight(ctx, c, "rootfolder", func(fctx context.Context) ([]RootFolder, error) {
		return fetchAll[RootFolder](fctx, c, apiPrefix+"/rootfolder")
	})
}
