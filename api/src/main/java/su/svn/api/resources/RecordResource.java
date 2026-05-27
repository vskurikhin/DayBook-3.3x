/*
 * This file was last modified at 2026.05.22 18:49 by Victor N. Skurikhin.
 * This is free and unencumbered software released into the public domain.
 * For more information, please refer to <http://unlicense.org>
 * RecordResource.java
 * $Id$
 */

package su.svn.api.resources;

import io.smallrye.mutiny.Uni;
import jakarta.annotation.security.PermitAll;
import jakarta.inject.Inject;
import jakarta.ws.rs.GET;
import jakarta.ws.rs.Path;
import jakarta.ws.rs.Produces;
import jakarta.ws.rs.QueryParam;
import jakarta.ws.rs.core.MediaType;
import org.eclipse.microprofile.openapi.annotations.Operation;
import org.eclipse.microprofile.openapi.annotations.media.Content;
import org.eclipse.microprofile.openapi.annotations.media.Schema;
import org.eclipse.microprofile.openapi.annotations.responses.APIResponse;
import su.svn.api.domain.enums.ResourcePath;
import su.svn.api.models.dto.RecordDataPage;
import su.svn.api.services.domain.JsonRecordDataService;
import su.svn.api.services.mappers.PageRecordDataMapper;

@Path(ResourcePath.RECORDS)
public class RecordResource {

    @Inject
    PageRecordDataMapper mapper;

    @Inject
    JsonRecordDataService service;

    @APIResponse(
            responseCode = "201",
            description = "Created",
            content = @Content(
                    mediaType = MediaType.APPLICATION_JSON,
                    schema = @Schema(
                            implementation = RecordDataPage.class
                    )
            )
    )
    @APIResponse(ref = "500Error")
    @PermitAll
    @Operation(summary = "Get page with list of JSON record")
    @GET
    @Produces(MediaType.APPLICATION_JSON)
    public Uni<RecordDataPage> page(@QueryParam("page") int page, @QueryParam("size") byte size) {
        return service.readPage(page, size)
                .log("RecordResource page")
                .map(postRecordPage -> mapper.toPage(postRecordPage));
    }
}
