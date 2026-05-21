package su.svn.api.resources;

import io.smallrye.mutiny.Uni;
import jakarta.ws.rs.core.Response;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.extension.ExtendWith;
import org.mockito.InjectMocks;
import org.mockito.Mock;
import org.mockito.junit.jupiter.MockitoExtension;
import su.svn.api.models.dto.NewBlobRecord;
import su.svn.api.models.dto.ResourceBlobRecord;
import su.svn.api.models.dto.UpdateBlobRecord;
import su.svn.api.services.domain.BlobRecordDataService;
import su.svn.api.services.schedulers.RecordSchedulerService;

import java.time.OffsetDateTime;
import java.util.UUID;

import static org.assertj.core.api.Assertions.assertThat;
import static org.mockito.Mockito.*;

@ExtendWith(MockitoExtension.class)
class BlobRecordResourceTest {

    @InjectMocks
    BlobRecordResource resource;

    @Mock
    BlobRecordDataService blobRecordDataService;

    @Mock
    RecordSchedulerService recordSchedulerService;

    @Test
    void shouldCreateRecord() {
        var request = NewBlobRecord.builder()
                .title("test")
                .postAt(OffsetDateTime.now())
                .build();

        var responseDto = ResourceBlobRecord.builder()
                .id(UUID.randomUUID())
                .title("test")
                .build();

        when(blobRecordDataService.post(request))
                .thenReturn(Uni.createFrom().item(responseDto));

        var response = resource.create(request)
                .await().indefinitely();

        assertThat(response.getStatus())
                .isEqualTo(Response.Status.CREATED.getStatusCode());

        assertThat(response.getEntity())
                .isEqualTo(responseDto);

        verify(blobRecordDataService).post(request);
        verify(recordSchedulerService).fire(true);
    }

    @Test
    void shouldDeleteRecord() {
        var id = UUID.randomUUID();

        when(blobRecordDataService.delete(id))
                .thenReturn(Uni.createFrom().voidItem());

        var response = resource.delete(id)
                .await().indefinitely();

        assertThat(response.getStatus())
                .isEqualTo(Response.Status.NO_CONTENT.getStatusCode());

        verify(blobRecordDataService).delete(id);
        verify(recordSchedulerService).fire(true);
    }

    @Test
    void shouldUpdateRecord() {
        var request = UpdateBlobRecord.builder()
                .id(UUID.randomUUID())
                .refreshAt(OffsetDateTime.now())
                .build();

        var responseDto = ResourceBlobRecord.builder()
                .id(request.id())
                .title("updated")
                .build();

        when(blobRecordDataService.put(request))
                .thenReturn(Uni.createFrom().item(responseDto));

        var response = resource.update(request)
                .await().indefinitely();

        assertThat(response.getStatus())
                .isEqualTo(Response.Status.OK.getStatusCode());

        assertThat(response.getEntity())
                .isEqualTo(responseDto);

        verify(blobRecordDataService).put(request);
        verify(recordSchedulerService).fire(true);
    }
}