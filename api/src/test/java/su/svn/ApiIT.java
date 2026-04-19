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
import org.junit.jupiter.api.Test;
import su.svn.api.model.dto.NewJsonRecord;
import su.svn.api.model.dto.ResourceJsonRecord;
import su.svn.api.model.dto.UpdateJsonRecord;
import su.svn.api.profile.ContainersProfile;
import su.svn.api.repository.client.rest.RecordViewClient;
import su.svn.api.resources.JsonRecordResource;
import su.svn.api.services.domain.RecordDataService;
import su.svn.api.services.schedulers.RecordSchedulerService;

import java.util.UUID;

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
    RecordDataService recordViewRepository;

    @Test
    @RunOnVertxContext
    void tests(UniAsserter asserter) {
        asserter.assertThat(
                () -> recordViewRepository.readPage(0, (byte) 127),
                recordView -> {
                    System.out.println("RESULT = " + recordView);
                }
        );
    }

    @Inject
    JsonRecordResource resource;

    @InjectMock
    RecordDataService recordDataService;

    @InjectMock
    RecordSchedulerService recordSchedulerService;

    @TestSecurity(user = "john", roles = {"USER"})
    @Test
    void jsonRecordResourceTest_shouldCreateRecord() {
        // given
        NewJsonRecord request = NewJsonRecord.builder().build();
        ResourceJsonRecord responseDto = ResourceJsonRecord.builder().build();

        when(recordDataService.post(request))
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
    void jsonRecordResourceTest_shouldDeleteRecord() {
        // given
        UUID id = UUID.randomUUID();

        when(recordDataService.delete(id))
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
    void jsonRecordResourceTest_shouldUpdateRecord() {
        // given
        UpdateJsonRecord request = UpdateJsonRecord.builder().build();
        ResourceJsonRecord responseDto = ResourceJsonRecord.builder().build();

        when(recordDataService.put(request))
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
    void jsonRecordResourceTest_shouldTriggerSchedulerOnlyAfterSuccess_create() {
        // given
        NewJsonRecord request = NewJsonRecord.builder().build();

        when(recordDataService.post(request))
                .thenReturn(Uni.createFrom().item(ResourceJsonRecord.builder().build()));

        // when
        resource.create(request).await().indefinitely();

        // then
        verify(recordSchedulerService, times(1)).fire(true);
    }

    @TestSecurity(user = "john", roles = {"USER"})
    @Test
    void jsonRecordResourceTest_shouldNotTriggerSchedulerOnFailure() {
        // given
        NewJsonRecord request = NewJsonRecord.builder().build();

        when(recordDataService.post(request))
                .thenReturn(Uni.createFrom().failure(new RuntimeException("boom")));

        // when / then
        try {
            resource.create(request).await().indefinitely();
        } catch (Exception ignored) {
        }

        verify(recordSchedulerService, never()).fire(true);
    }
}
