/*
 * This file was last modified at 2026.04.15 20:40 by Victor N. Skurikhin.
 * This is free and unencumbered software released into the public domain.
 * For more information, please refer to <http://unlicense.org>
 * JsonRecordRepository.java
 * $Id$
 */

package su.svn.api.repository;

import io.smallrye.mutiny.Uni;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import org.eclipse.microprofile.config.inject.ConfigProperty;
import org.eclipse.microprofile.rest.client.inject.RestClient;
import su.svn.api.model.dto.NewJsonRecord;
import su.svn.api.model.dto.ResourceJsonRecord;
import su.svn.api.model.dto.UpdateJsonRecord;
import su.svn.api.repository.client.rest.JsonRecordClient;
import su.svn.api.services.security.SecurityContextPrincipalHelper;

import java.util.UUID;

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
