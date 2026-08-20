package arrapi

import "context"

// QualityProfiles returns every quality profile defined on the instance.
func (c *client) QualityProfiles(ctx context.Context) ([]QualityProfile, error) {
	return c.fetchAll[QualityProfile](ctx, apiPrefix+"/qualityprofile")
}

// RootFolders returns every root folder configured on the instance.
func (c *client) RootFolders(ctx context.Context) ([]RootFolder, error) {
	return c.fetchAll[RootFolder](ctx, apiPrefix+"/rootfolder")
}
