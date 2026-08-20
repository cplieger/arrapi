package arrapi

import "context"

// GetQualityProfiles returns every quality profile defined on the instance.
func (c *client) GetQualityProfiles(ctx context.Context) ([]QualityProfile, error) {
	return c.fetchAll[QualityProfile](ctx, apiPrefix+"/qualityprofile")
}

// GetRootFolders returns every root folder configured on the instance.
func (c *client) GetRootFolders(ctx context.Context) ([]RootFolder, error) {
	return c.fetchAll[RootFolder](ctx, apiPrefix+"/rootfolder")
}
