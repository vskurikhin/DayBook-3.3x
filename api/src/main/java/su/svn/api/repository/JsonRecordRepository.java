/*
 * This file was last modified at 2026.04.05 22:27 by Victor N. Skurikhin.
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

import java.util.UUID;

@ApplicationScoped
public class JsonRecordRepository {

    @Inject
    @RestClient
    JsonRecordClient jsonRecordClient;

    public Uni<Void> delete(UUID id) {
        return jsonRecordClient.delete(id);
    }

    public Uni<ResourceJsonRecord> post(NewJsonRecord newJsonRecord) {
        return jsonRecordClient.post(newJsonRecord);
    }

    public Uni<ResourceJsonRecord> put(UpdateJsonRecord updateJsonRecord) {
        return jsonRecordClient.put(updateJsonRecord);
    }
}
