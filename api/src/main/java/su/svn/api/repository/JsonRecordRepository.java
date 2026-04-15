/*
 * This file was last modified at 2026.04.20 00:29 by Victor N. Skurikhin.
 * This is free and unencumbered software released into the public domain.
 * For more information, please refer to <http://unlicense.org>
 * JsonRecordRepository.java
 * $Id$
 */

package su.svn.api.repository;

import io.smallrye.mutiny.Uni;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import org.eclipse.microprofile.rest.client.inject.RestClient;
import su.svn.api.model.dto.NewJsonRecord;
import su.svn.api.model.dto.ResourceJsonRecord;
import su.svn.api.model.dto.UpdateJsonRecord;
import su.svn.api.repository.client.rest.JsonRecordClient;
import su.svn.api.services.security.SecurityContextPrincipalHelper;

import java.util.UUID;

/**
 * Repository layer responsible for interacting with the remote JSON Record service.
 *
 * <p>This class delegates CRUD operations to {@link JsonRecordClient} and enriches
 * each request with an Authorization header obtained from
 * {@link SecurityContextPrincipalHelper}.
 *
 * <p>The repository operates in a reactive, non-blocking manner using {@link Uni}.
 *
 * <p>Authorization flow:
 * <ul>
 *     <li>Fetches JWT-based authorization header via {@link SecurityContextPrincipalHelper#authorization()}</li>
 *     <li>Passes it to downstream REST client</li>
 * </ul>
 *
 * <p>All methods return {@link Uni} and must be subscribed to in order to trigger execution.
 *
 * <p>This class is application-scoped and intended to be injected as a CDI bean.
 */
@ApplicationScoped
public class JsonRecordRepository {
    @Inject
    @RestClient
    JsonRecordClient jsonRecordClient;

    @Inject
    SecurityContextPrincipalHelper principalHelper;

    public Uni<Void> delete(UUID id) {
        var authorization = principalHelper.authorization();
        return jsonRecordClient.delete(authorization, id);
    }

    public Uni<ResourceJsonRecord> post(NewJsonRecord newJsonRecord) {
        var authorization = principalHelper.authorization();
        return jsonRecordClient.post(authorization, newJsonRecord);
    }

    public Uni<ResourceJsonRecord> put(UpdateJsonRecord updateJsonRecord) {
        var authorization = principalHelper.authorization();
        return jsonRecordClient.put(authorization, updateJsonRecord);
    }
}
