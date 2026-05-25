package su.svn.api.resources;

import io.smallrye.mutiny.Uni;
import jakarta.ws.rs.core.Response;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.extension.ExtendWith;
import org.mockito.InjectMocks;
import org.mockito.Mock;
import org.mockito.junit.jupiter.MockitoExtension;
import su.svn.api.models.dto.NewVectorRecord;
import su.svn.api.models.dto.ResourceVectorRecord;
import su.svn.api.models.dto.UpdateVectorRecord;
import su.svn.api.services.domain.VectorRecordDataService;
import su.svn.api.services.schedulers.RecordSchedulerService;

import java.time.OffsetDateTime;
import java.util.UUID;

import static org.assertj.core.api.Assertions.assertThat;
import static org.mockito.Mockito.*;

@ExtendWith(MockitoExtension.class)
class VectorRecordResourceTest {

    @InjectMocks
    VectorRecordResource resource;

    @Mock
    VectorRecordDataService service;

    @Mock
    RecordSchedulerService schedulerService;

    @Test
    void shouldCreateRecord() {
        var request = NewVectorRecord.builder()
                .parentId(UUID.randomUUID())
                .title("vector")
                .vector(new float[]{1.0f, 2.0f})
                .postAt(OffsetDateTime.now())
                .visible(true)
                .flags(5)
                .build();

        var responseDto = ResourceVectorRecord.builder()
                .id(UUID.randomUUID())
                .title("vector")
                .vector(new float[]{1.0f, 2.0f})
                .visible(true)
                .flags(5)
                .build();

        when(service.post(request))
                .thenReturn(Uni.createFrom().item(responseDto));

        Response response = resource.create(request)
                .await()
                .indefinitely();

        assertThat(response.getStatus())
                .isEqualTo(Response.Status.CREATED.getStatusCode());

        assertThat(response.getEntity())
                .isEqualTo(responseDto);

        verify(service).post(request);
        verify(schedulerService).fire(true);
    }

    @Test
    void shouldDeleteRecord() {
        var id = UUID.randomUUID();

        when(service.delete(id))
                .thenReturn(Uni.createFrom().voidItem());

        Response response = resource.delete(id)
                .await()
                .indefinitely();

        assertThat(response.getStatus())
                .isEqualTo(Response.Status.NO_CONTENT.getStatusCode());

        verify(service).delete(id);
        verify(schedulerService).fire(true);
    }

    @Test
    void shouldUpdateRecord() {
        var id = UUID.randomUUID();

        var request = UpdateVectorRecord.builder()
                .id(id)
                .title("updated")
                .vector(new float[]{3.0f, 4.0f})
                .refreshAt(OffsetDateTime.now())
                .visible(true)
                .flags(9)
                .build();

        var responseDto = ResourceVectorRecord.builder()
                .id(id)
                .title("updated")
                .vector(new float[]{3.0f, 4.0f})
                .visible(true)
                .flags(9)
                .build();

        when(service.put(request))
                .thenReturn(Uni.createFrom().item(responseDto));

        Response response = resource.update(request)
                .await()
                .indefinitely();

        assertThat(response.getStatus())
                .isEqualTo(Response.Status.OK.getStatusCode());

        assertThat(response.getEntity())
                .isEqualTo(responseDto);

        verify(service).put(request);
        verify(schedulerService).fire(true);
    }
}