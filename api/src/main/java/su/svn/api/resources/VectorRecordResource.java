/*
 * This file was last modified at 2026.05.22 18:49 by Victor N. Skurikhin.
 * This is free and unencumbered software released into the public domain.
 * For more information, please refer to <http://unlicense.org>
 * VectorRecordResource.java
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
import su.svn.api.models.dto.NewVectorRecord;
import su.svn.api.models.dto.UpdateVectorRecord;
import su.svn.api.services.domain.VectorRecordDataService;
import su.svn.api.services.schedulers.RecordSchedulerService;

import java.util.UUID;

/**
 * REST resource for managing vector records.
 *
 * <p>
 * This resource provides secured reactive HTTP endpoints for:
 * </p>
 * <ul>
 *     <li>Creating vector records</li>
 *     <li>Updating vector records</li>
 *     <li>Deleting vector records</li>
 * </ul>
 *
 * <p>
 * All operations are delegated to {@link VectorRecordDataService}
 * and executed asynchronously using Mutiny {@link Uni}.
 * </p>
 *
 * <h2>Security</h2>
 * <p>
 * All endpoints require the {@code USER} role.
 * </p>
 *
 * <h2>Scheduler Integration</h2>
 * <p>
 * After successful create, update, or delete operations,
 * {@link RecordSchedulerService#fire(boolean)} is triggered
 * in order to notify the scheduling subsystem about record changes.
 * </p>
 *
 * <h2>Media Types</h2>
 * <p>
 * The resource consumes and produces JSON payloads.
 * </p>
 *
 * @see VectorRecordDataService
 * @see RecordSchedulerService
 * @see io.smallrye.mutiny.Uni
 * @see jakarta.ws.rs.core.Response
 */
@Path(ResourcePath.RECORD + "/vector")
public class VectorRecordResource {

    /**
     * Service responsible for vector record business logic.
     */
    @Inject
    VectorRecordDataService service;

    /**
     * Scheduler service responsible for propagating record updates.
     */
    @Inject
    RecordSchedulerService schedulerService;

    /**
     * Creates a new vector record.
     *
     * <p>
     * The request is delegated to the vector data service.
     * After successful creation the scheduler service is notified.
     * </p>
     *
     * @param entry DTO containing vector record creation data
     * @return a {@link Uni} emitting HTTP 201 response
     *         with the created vector resource
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
    public Uni<Response> create(NewVectorRecord entry) {
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
     * Deletes an existing vector record.
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
     * Updates an existing vector record.
     *
     * <p>
     * The request is delegated to the vector data service.
     * After successful update the scheduler service is notified.
     * </p>
     *
     * @param entry DTO containing updated vector record data
     * @return a {@link Uni} emitting HTTP 200 response
     *         with the updated vector resource
     */
    @APIResponse(ref = "200OK")
    @APIResponse(ref = "500Error")
    @RolesAllowed("USER")
    @Operation(summary = "Update BLOB record")
    @PUT
    @Path(ResourcePath.NONE)
    @Consumes(MediaType.APPLICATION_JSON)
    @Produces(MediaType.APPLICATION_JSON)
    public Uni<Response> update(UpdateVectorRecord entry) {
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
