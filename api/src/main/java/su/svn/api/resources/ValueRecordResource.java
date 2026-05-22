/*
 * This file was last modified at 2026.05.22 18:49 by Victor N. Skurikhin.
 * This is free and unencumbered software released into the public domain.
 * For more information, please refer to <http://unlicense.org>
 * ValueRecordResource.java
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
import su.svn.api.models.dto.NewValueRecord;
import su.svn.api.models.dto.UpdateValueRecord;
import su.svn.api.services.domain.ValueRecordDataService;
import su.svn.api.services.schedulers.RecordSchedulerService;

import java.util.UUID;

/**
 * Reactive REST resource for managing value records.
 *
 * <p>
 * Provides endpoints for creating, updating, and deleting
 * value-based records within the system.
 * </p>
 *
 * <p>
 * All operations are asynchronous and implemented
 * using Mutiny {@link Uni}.
 * </p>
 *
 * <p>
 * Successful modification operations trigger
 * record synchronization through the scheduler service.
 * </p>
 */
@Path(ResourcePath.RECORD + "/value")
public class ValueRecordResource {

    /**
     * Service responsible for value record business logic.
     */
    @Inject
    ValueRecordDataService service;

    /**
     * Scheduler service responsible for propagating record updates.
     */
    @Inject
    RecordSchedulerService schedulerService;

    /**
     * Creates a new value record.
     *
     * @param entry DTO containing data for the new value record
     * @return asynchronous HTTP response containing the created record
     *         with status {@code 201 Created}
     */
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
    public Uni<Response> create(NewValueRecord entry) {
        return service.post(entry)
                .map(resourceJsonRecord ->
                        Response.status(Response.Status.CREATED)
                                .entity(resourceJsonRecord)
                                .build()
                )
                .onItem()
                .invoke(() -> schedulerService.fire(true));
    }

    /**
     * Deletes an existing value record.
     *
     * <p>
     * Performs a logical deletion operation and triggers
     * record synchronization.
     * </p>
     *
     * @param id unique identifier of the record
     * @return asynchronous HTTP response with status
     *         {@code 204 No Content}
     */
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
        return service.delete(id)
                .map(resourceJsonRecord ->
                        Response.status(Response.Status.NO_CONTENT).build()
                )
                .onItem()
                .invoke(() -> schedulerService.fire(true));
    }

    /**
     * Updates an existing value record.
     *
     * @param entry DTO containing updated record data
     * @return asynchronous HTTP response containing the updated record
     *         with status {@code 200 OK}
     */
    @APIResponse(ref = "200OK")
    @APIResponse(ref = "500Error")
    @RolesAllowed("USER")
    @Operation(summary = "Update BLOB record")
    @PUT
    @Path(ResourcePath.NONE)
    @Consumes(MediaType.APPLICATION_JSON)
    @Produces(MediaType.APPLICATION_JSON)
    public Uni<Response> update(UpdateValueRecord entry) {
        return service.put(entry)
                .map(resourceJsonRecord ->
                        Response.status(Response.Status.OK)
                                .entity(resourceJsonRecord)
                                .build()
                )
                .onItem()
                .invoke(() -> schedulerService.fire(true));
    }
}
