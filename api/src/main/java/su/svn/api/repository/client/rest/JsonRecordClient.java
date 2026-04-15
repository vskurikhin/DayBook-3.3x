/*
 * This file was last modified at 2026.04.15 20:40 by Victor N. Skurikhin.
 * This is free and unencumbered software released into the public domain.
 * For more information, please refer to <http://unlicense.org>
 * JsonRecordClient.java
 * $Id$
 */

package su.svn.api.repository.client.rest;

import io.smallrye.mutiny.Uni;
import jakarta.ws.rs.*;
import jakarta.ws.rs.core.MediaType;
import org.eclipse.microprofile.rest.client.inject.RegisterRestClient;
import su.svn.api.domain.enums.ResourcePath;
import su.svn.api.model.dto.NewJsonRecord;
import su.svn.api.model.dto.ResourceJsonRecord;
import su.svn.api.model.dto.UpdateJsonRecord;

import java.util.UUID;

@Path("/core/api/v2/json-record")
@RegisterRestClient
public interface JsonRecordClient {
    @DELETE
    @Path("/" + ResourcePath.ID)
    @Consumes(MediaType.APPLICATION_JSON)
    Uni<Void> delete(@HeaderParam("Authorization") String authorization, UUID id);

    @POST
    @Consumes(MediaType.APPLICATION_JSON)
    Uni<ResourceJsonRecord> post(@HeaderParam("Authorization") String authorization, NewJsonRecord newJsonRecord);

    @PUT
    @Consumes(MediaType.APPLICATION_JSON)
    Uni<ResourceJsonRecord> put(@HeaderParam("Authorization") String authorization, UpdateJsonRecord updateJsonRecord);
}
