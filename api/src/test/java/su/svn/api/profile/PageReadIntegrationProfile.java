package su.svn.api.profile;

import su.svn.api.resources.PageReadIntegrationResource;

import java.util.List;

/** Excludes the legacy global DB fixture so both drivers use this test's database. */
public class PageReadIntegrationProfile extends ContainersProfile {
    @Override
    public boolean disableGlobalTestResources() {
        return true;
    }

    @Override
    public List<TestResourceEntry> testResources() {
        return List.of(new TestResourceEntry(PageReadIntegrationResource.class));
    }
}
