/*
 * This file was last modified at 2026.04.20 00:29 by Victor N. Skurikhin.
 * This is free and unencumbered software released into the public domain.
 * For more information, please refer to <http://unlicense.org>
 * JsonRecordResource.java
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
import su.svn.api.domain.enums.ResourcePath;
import su.svn.api.model.dto.NewJsonRecord;
import su.svn.api.model.dto.UpdateJsonRecord;
import su.svn.api.services.domain.RecordDataService;
import su.svn.api.services.schedulers.RecordSchedulerService;

import java.util.UUID;

/**
 * REST resource for managing JSON records.
 *
 * <p>This resource exposes CRUD operations for JSON records and delegates
 * the business logic to {@link RecordDataService}. After each successful
 * mutation (create, update, delete), it triggers {@link RecordSchedulerService}.
 *
 * <p>All endpoints are protected with {@link RolesAllowed} and require the "USER" role.
 *
 * <p>Reactive behavior:
 * <ul>
 *     <li>All methods return {@link Uni} and are non-blocking</li>
 *     <li>Scheduler trigger is executed as a side-effect using {@code onItem().invoke(...)}</li>
 * </ul>
 *
 * <p>Endpoints:
 * <ul>
 *     <li>{@code POST /record/json} — create a new record</li>
 *     <li>{@code DELETE /record/json/{id}} — delete a record</li>
 *     <li>{@code PUT /record/json} — update a record</li>
 * </ul>
 */
@Path(ResourcePath.RECORD + "/json")
public class JsonRecordResource {

    @Inject
    RecordDataService recordDataService;

    @Inject
    RecordSchedulerService recordSchedulerService;

    @RolesAllowed("USER")
    @Operation(summary = "Create JSON record")
    @POST
    @Path(ResourcePath.NONE)
    @Consumes(MediaType.APPLICATION_JSON)
    @Produces(MediaType.APPLICATION_JSON)
    public Uni<Response> create(NewJsonRecord entry) {
        return recordDataService.post(entry)
                .map(resourceJsonRecord ->
                        Response.status(Response.Status.CREATED)
                                .entity(resourceJsonRecord)
                                .build()
                )
                .onItem()
                .invoke(() -> recordSchedulerService.fire(true));
    }

    @RolesAllowed("USER")
    @Operation(summary = "Delete JSON record")
    @DELETE
    @Path(ResourcePath.ID)
    @Produces(MediaType.APPLICATION_JSON)
    public Uni<Response> delete(UUID id) {
        return recordDataService.delete(id)
                .map(resourceJsonRecord ->
                        Response.status(Response.Status.NO_CONTENT).build()
                )
                .onItem()
                .invoke(() -> recordSchedulerService.fire(true));
    }

    @RolesAllowed("USER")
    @Operation(summary = "Update JSON record")
    @PUT
    @Path(ResourcePath.NONE)
    @Consumes(MediaType.APPLICATION_JSON)
    @Produces(MediaType.APPLICATION_JSON)
    public Uni<Response> update(UpdateJsonRecord entry) {
        return recordDataService.put(entry)
                .map(resourceJsonRecord ->
                        Response.status(Response.Status.OK)
                                .entity(resourceJsonRecord)
                                .build()
                )
                .onItem()
                .invoke(() -> recordSchedulerService.fire(true));
    }
}
