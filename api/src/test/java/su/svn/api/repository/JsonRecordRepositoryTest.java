package su.svn.api.repository;

import io.quarkus.test.InjectMock;
import io.quarkus.test.junit.QuarkusTest;
import io.quarkus.test.junit.TestProfile;
import io.smallrye.mutiny.Uni;
import jakarta.inject.Inject;
import org.eclipse.microprofile.rest.client.inject.RestClient;
import org.junit.jupiter.api.Test;
import su.svn.api.model.dto.NewJsonRecord;
import su.svn.api.model.dto.ResourceJsonRecord;
import su.svn.api.model.dto.UpdateJsonRecord;
import su.svn.api.profile.NoContainersProfile;
import su.svn.api.repository.client.rest.JsonRecordClient;
import su.svn.api.services.security.SecurityContextPrincipalHelper;

import java.util.UUID;

import static org.assertj.core.api.Assertions.assertThat;
import static org.mockito.ArgumentMatchers.anyString;
import static org.mockito.Mockito.*;

@QuarkusTest
@TestProfile(NoContainersProfile.class)
class JsonRecordRepositoryTest {

    @Inject
    JsonRecordRepository repository;

    @InjectMock
    @RestClient
    JsonRecordClient client;

    @InjectMock
    SecurityContextPrincipalHelper principalHelper;

    private static final String AUTH = "Bearer test-token";

    @Test
    void shouldDeleteRecord() {
        // given
        UUID id = UUID.randomUUID();
        when(principalHelper.authorization()).thenReturn(AUTH);
        when(client.delete(eq(AUTH), anyString(), eq(id))).thenReturn(Uni.createFrom().voidItem());

        // when
        repository.delete(id).await().indefinitely();

        // then
        verify(client).delete(eq(AUTH), anyString(), eq(id));
    }

    @Test
    void shouldPostRecord() {
        // given
        NewJsonRecord request = NewJsonRecord.builder().build();
        ResourceJsonRecord response = ResourceJsonRecord.builder().build();

        when(principalHelper.authorization()).thenReturn(AUTH);
        when(client.post(eq(AUTH), anyString(), eq(request))).thenReturn(Uni.createFrom().item(response));
        org.jboss.logging.MDC.put("REQUEST_ID", UUID.randomUUID().toString());

        // when
        ResourceJsonRecord result = repository.post(request).await().indefinitely();

        // then
        assertThat(result).isEqualTo(response);
        verify(client).post(eq(AUTH), anyString(), eq(request));
    }

    @Test
    void shouldPutRecord() {
        // given
        UpdateJsonRecord request = UpdateJsonRecord.builder().build();
        ResourceJsonRecord response = ResourceJsonRecord.builder().build();

        when(principalHelper.authorization()).thenReturn(AUTH);
        when(client.put(eq(AUTH), anyString(), eq(request))).thenReturn(Uni.createFrom().item(response));
        org.jboss.logging.MDC.put("REQUEST_ID", UUID.randomUUID().toString());

        // when
        ResourceJsonRecord result = repository.put(request).await().indefinitely();

        // then
        assertThat(result).isEqualTo(response);
        verify(client).put(eq(AUTH), anyString(), eq(request));
    }

    @Test
    void shouldUseAuthorizationFromHelper() {
        // given
        UUID id = UUID.randomUUID();
        when(principalHelper.authorization()).thenReturn(AUTH);
        when(client.delete(eq(AUTH), anyString(), eq(id))).thenReturn(Uni.createFrom().voidItem());
        org.jboss.logging.MDC.put("REQUEST_ID", UUID.randomUUID().toString());

        // when
        repository.delete(id).await().indefinitely();

        // then
        verify(principalHelper, times(1)).authorization();
    }
}