/*
 * This file was last modified at 2026.05.21 23:42 by Victor N. Skurikhin.
 * This is free and unencumbered software released into the public domain.
 * For more information, please refer to <http://unlicense.org>
 * SetRecordResource.java
 * $Id$
 */

package su.svn.api.resources;

import io.smallrye.mutiny.Uni;
import jakarta.annotation.security.RolesAllowed;
import jakarta.inject.Inject;
import jakarta.ws.rs.*;
import jakarta.ws.rs.core.MediaType;
import jakarta.ws.rs.core.Response;
import org.eclipse.microprofile.openapi.annotations.Operation;
import org.eclipse.microprofile.openapi.annotations.responses.APIResponse;
import su.svn.api.domain.enums.ResourcePath;
import su.svn.api.models.dto.NewSetRecord;
import su.svn.api.models.dto.UpdateSetRecord;
import su.svn.api.services.domain.SetRecordDataService;
import su.svn.api.services.schedulers.RecordSchedulerService;

import java.util.UUID;

@Path(ResourcePath.RECORD + "/set")
public class SetRecordResource {

    @Inject
    SetRecordDataService blobRecordDataService;

    @Inject
    RecordSchedulerService recordSchedulerService;

    @APIResponse(
            responseCode = "201",
            description = "Created"
    )
    @APIResponse(ref = "500Error")
    @RolesAllowed("USER")
    @Operation(summary = "Create BLOB record")
    @POST
    @Path(ResourcePath.NONE)
    @Consumes(MediaType.APPLICATION_JSON)
    @Produces(MediaType.APPLICATION_JSON)
    public Uni<Response> create(NewSetRecord entry) {
        return blobRecordDataService.post(entry)
                .map(resourceJsonRecord ->
                        Response.status(Response.Status.CREATED)
                                .entity(resourceJsonRecord)
                                .build()
                )
                .onItem()
                .invoke(() -> recordSchedulerService.fire(true));
    }

    @APIResponse(
            responseCode = "204",
            description = "No Content"
    )
    @APIResponse(ref = "500Error")
    @RolesAllowed("USER")
    @Operation(summary = "Delete BLOB record")
    @DELETE
    @Path(ResourcePath.ID)
    @Produces(MediaType.APPLICATION_JSON)
    public Uni<Response> delete(UUID id) {
        return blobRecordDataService.delete(id)
                .map(resourceJsonRecord ->
                        Response.status(Response.Status.NO_CONTENT).build()
                )
                .onItem()
                .invoke(() -> recordSchedulerService.fire(true));
    }

    @APIResponse(ref = "200OK")
    @APIResponse(ref = "500Error")
    @RolesAllowed("USER")
    @Operation(summary = "Update BLOB record")
    @PUT
    @Path(ResourcePath.NONE)
    @Consumes(MediaType.APPLICATION_JSON)
    @Produces(MediaType.APPLICATION_JSON)
    public Uni<Response> update(UpdateSetRecord entry) {
        return blobRecordDataService.put(entry)
                .map(resourceJsonRecord ->
                        Response.status(Response.Status.OK)
                                .entity(resourceJsonRecord)
                                .build()
                )
                .onItem()
                .invoke(() -> recordSchedulerService.fire(true));
    }
}
