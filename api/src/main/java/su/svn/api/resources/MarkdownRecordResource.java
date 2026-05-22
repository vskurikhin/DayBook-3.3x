/*
 * This file was last modified at 2026.05.22 18:49 by Victor N. Skurikhin.
 * This is free and unencumbered software released into the public domain.
 * For more information, please refer to <http://unlicense.org>
 * MarkdownRecordResource.java
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
import su.svn.api.models.dto.NewMarkdownRecord;
import su.svn.api.models.dto.UpdateMarkdownRecord;
import su.svn.api.services.domain.MarkdownRecordDataService;
import su.svn.api.services.schedulers.RecordSchedulerService;

import java.util.UUID;

/**
 * Reactive REST resource for markdown record management.
 *
 * <p>
 * Provides REST endpoints for:
 * </p>
 * <ul>
 *     <li>creating markdown records</li>
 *     <li>updating markdown records</li>
 *     <li>deleting markdown records</li>
 * </ul>
 *
 * <p>
 * All operations are implemented using reactive Mutiny {@link Uni} types
 * and return asynchronous HTTP responses.
 * </p>
 *
 * <h2>Security</h2>
 * <p>
 * All endpoints require the {@code USER} role.
 * </p>
 *
 * <h2>Scheduler Integration</h2>
 * <p>
 * After each successful modification operation, the
 * {@link RecordSchedulerService} is triggered to initiate
 * synchronization or refresh processing.
 * </p>
 *
 * @see MarkdownRecordDataService
 * @see RecordSchedulerService
 * @see io.smallrye.mutiny.Uni
 */
@Path(ResourcePath.RECORD + "/markdown")
public class MarkdownRecordResource {

    /**
     * Service responsible for markdown record business operations.
     */
    @Inject
    MarkdownRecordDataService service;

    /**
     * Scheduler service responsible for triggering record synchronization tasks.
     */
    @Inject
    RecordSchedulerService schedulerService;

    /**
     * Creates a new markdown record.
     *
     * <p>
     * Accepts markdown record data in JSON format and returns
     * the created resource with HTTP status {@code 201 Created}.
     * </p>
     *
     * <p>
     * After successful creation, the scheduler service is triggered.
     * </p>
     *
     * @param entry DTO containing markdown record creation data
     * @return reactive HTTP response containing the created markdown resource
     */
    @APIResponse(
            responseCode = "201",
            description = "Created"
    )
    @APIResponse(ref = "500Error")
    @RolesAllowed("USER")
    @Operation(summary = "Create markdown record")
    @POST
    @Path(ResourcePath.NONE)
    @Consumes(MediaType.APPLICATION_JSON)
    @Produces(MediaType.APPLICATION_JSON)
    public Uni<Response> create(NewMarkdownRecord entry) {
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
     * Deletes an existing markdown record.
     *
     * <p>
     * Performs a logical deletion of the markdown record identified by
     * the specified UUID and returns HTTP status {@code 204 No Content}.
     * </p>
     *
     * <p>
     * After successful deletion, the scheduler service is triggered.
     * </p>
     *
     * @param id unique identifier of the markdown record
     * @return reactive HTTP response with no content
     */
    @APIResponse(
            responseCode = "204",
            description = "No Content"
    )
    @APIResponse(ref = "500Error")
    @RolesAllowed("USER")
    @Operation(summary = "Delete markdown record")
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
     * Updates an existing markdown record.
     *
     * <p>
     * Accepts updated markdown record data in JSON format and returns
     * the updated resource with HTTP status {@code 200 OK}.
     * </p>
     *
     * <p>
     * After successful update, the scheduler service is triggered.
     * </p>
     *
     * @param entry DTO containing updated markdown record data
     * @return reactive HTTP response containing the updated markdown resource
     */
    @APIResponse(ref = "200OK")
    @APIResponse(ref = "500Error")
    @RolesAllowed("USER")
    @Operation(summary = "Update markdown record")
    @PUT
    @Path(ResourcePath.NONE)
    @Consumes(MediaType.APPLICATION_JSON)
    @Produces(MediaType.APPLICATION_JSON)
    public Uni<Response> update(UpdateMarkdownRecord entry) {
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