package su.svn;

import io.quarkus.test.InjectMock;
import io.quarkus.test.common.QuarkusTestResource;
import io.quarkus.test.junit.QuarkusTest;
import io.quarkus.test.junit.TestProfile;
import io.quarkus.test.security.TestSecurity;
import io.quarkus.test.vertx.RunOnVertxContext;
import io.quarkus.test.vertx.UniAsserter;
import io.smallrye.mutiny.Uni;
import jakarta.inject.Inject;
import jakarta.ws.rs.core.Response;
import org.eclipse.microprofile.rest.client.inject.RestClient;
import org.junit.jupiter.api.*;
import su.svn.api.domain.entities.PostRecord;
import su.svn.api.models.dto.NewJsonRecord;
import su.svn.api.models.dto.Page;
import su.svn.api.models.dto.ResourceJsonRecord;
import su.svn.api.models.dto.UpdateJsonRecord;
import su.svn.api.profile.ContainersProfile;
import su.svn.api.repository.client.rest.RecordViewClient;
import su.svn.api.resources.JsonRecordResource;
import su.svn.api.services.domain.JsonRecordDataService;
import su.svn.api.services.schedulers.RecordSchedulerService;

import java.util.UUID;
import java.util.function.Consumer;

import static org.assertj.core.api.Assertions.assertThat;
import static org.mockito.Mockito.*;

@QuarkusTest
@QuarkusTestResource(value = PostgreSQLTestResource.class, restrictToAnnotatedClass = true)
@TestProfile(ContainersProfile.class)
public class ApiIT {

    @InjectMock
    @RestClient
    RecordViewClient mockRecordViewClient;

    @Inject
    JsonRecordDataService recordViewRepository;

    @Inject
    JsonRecordResource resource;

    @InjectMock
    JsonRecordDataService jsonRecordDataService;

    @InjectMock
    RecordSchedulerService recordSchedulerService;

    @BeforeEach
    void beforeEach(TestInfo testInfo) {
        System.err.println("Running: " + testInfo.getDisplayName());
    }

    @Test
    @DisplayName("RecordViewRepository read page")
    @RunOnVertxContext
    void tests(UniAsserter asserter) {
        asserter.assertThat(
                () -> recordViewRepository.readPage(0, (byte) 127),
                new Consumer<Page<PostRecord>>() {
                    @Override
                    public void accept(Page<PostRecord> postRecordPage) {
                        System.out.println(postRecordPage);
                    }
                }
        );
    }

    @TestSecurity(user = "john", roles = {"USER"})
    @Test
    @DisplayName("JsonRecordResource create")
    void jsonRecordResourceTest_shouldCreateRecord() {
        // given
        NewJsonRecord request = NewJsonRecord.builder().build();
        ResourceJsonRecord responseDto = ResourceJsonRecord.builder().build();

        when(jsonRecordDataService.post(request))
                .thenReturn(Uni.createFrom().item(responseDto));

        // when
        Response response = resource.create(request)
                .await().indefinitely();

        // then
        assertThat(response.getStatus()).isEqualTo(Response.Status.CREATED.getStatusCode());
        assertThat(response.getEntity()).isEqualTo(responseDto);

        verify(recordSchedulerService).fire(true);
    }

    @TestSecurity(user = "john", roles = {"USER"})
    @Test
    @DisplayName("JsonRecordResource delete")
    void jsonRecordResourceTest_shouldDeleteRecord() {
        // given
        UUID id = UUID.randomUUID();

        when(jsonRecordDataService.delete(id))
                .thenReturn(Uni.createFrom().voidItem());

        // when
        Response response = resource.delete(id)
                .await().indefinitely();

        // then
        assertThat(response.getStatus()).isEqualTo(Response.Status.NO_CONTENT.getStatusCode());

        verify(recordSchedulerService).fire(true);
    }

    @TestSecurity(user = "john", roles = {"USER"})
    @Test
    @DisplayName("JsonRecordResource update")
    void jsonRecordResourceTest_shouldUpdateRecord() {
        // given
        UpdateJsonRecord request = UpdateJsonRecord.builder().build();
        ResourceJsonRecord responseDto = ResourceJsonRecord.builder().build();

        when(jsonRecordDataService.put(request))
                .thenReturn(Uni.createFrom().item(responseDto));

        // when
        Response response = resource.update(request)
                .await().indefinitely();

        // then
        assertThat(response.getStatus()).isEqualTo(Response.Status.OK.getStatusCode());
        assertThat(response.getEntity()).isEqualTo(responseDto);

        verify(recordSchedulerService).fire(true);
    }

    @TestSecurity(user = "john", roles = {"USER"})
    @Test
    @DisplayName("JsonRecordResource should trigger scheduler only after success create")
    void jsonRecordResourceTest_shouldTriggerSchedulerOnlyAfterSuccess_create() {
        // given
        NewJsonRecord request = NewJsonRecord.builder().build();

        when(jsonRecordDataService.post(request))
                .thenReturn(Uni.createFrom().item(ResourceJsonRecord.builder().build()));

        // when
        resource.create(request).await().indefinitely();

        // then
        verify(recordSchedulerService, times(1)).fire(true);
    }

    @TestSecurity(user = "john", roles = {"USER"})
    @Test
    @DisplayName("JsonRecordResource should not trigger scheduler on failure")
    void jsonRecordResourceTest_shouldNotTriggerSchedulerOnFailure() {
        // given
        NewJsonRecord request = NewJsonRecord.builder().build();

        when(jsonRecordDataService.post(request))
                .thenReturn(Uni.createFrom().failure(new RuntimeException("boom")));

        // when / then
        try {
            resource.create(request).await().indefinitely();
        } catch (Exception ignored) {
        }

        verify(recordSchedulerService, never()).fire(true);
    }
}
