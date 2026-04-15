package su.svn.api.services.schedulers;

import io.quarkus.test.InjectMock;
import io.quarkus.test.junit.QuarkusTest;
import io.quarkus.test.junit.TestProfile;
import io.quarkus.test.vertx.UniAsserter;
import io.smallrye.mutiny.Uni;
import jakarta.inject.Inject;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import su.svn.api.domain.entities.PostRecord;
import su.svn.api.profile.NoContainersProfile;
import su.svn.api.services.domain.RecordDataService;

import java.util.List;

import static org.mockito.ArgumentMatchers.anyInt;
import static org.mockito.Mockito.when;

@QuarkusTest
@TestProfile(NoContainersProfile.class)
class RecordSchedulerServiceTest {

    @InjectMock
    RecordDataService mockRecordDataService;

    @Inject
    RecordSchedulerService recordSchedulerService;

    @BeforeEach
    public void setUp() {
        when(mockRecordDataService.sync(anyInt(), anyInt())).thenReturn(Uni.createFrom().item(List.of(PostRecord.builder().build())));
    }

    @Test
    void tests(UniAsserter asserter) throws InterruptedException {
        recordSchedulerService.fire(true);
        Thread.sleep(15 * 1000);
    }
}