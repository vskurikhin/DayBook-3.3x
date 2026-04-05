/*
 * This file was last modified at 2026.04.05 22:27 by Victor N. Skurikhin.
 * This is free and unencumbered software released into the public domain.
 * For more information, please refer to <http://unlicense.org>
 * RecordResource.java
 * $Id$
 */

package su.svn.api.resources;

import io.smallrye.mutiny.Uni;
import io.smallrye.mutiny.tuples.Tuple2;
import jakarta.inject.Inject;
import jakarta.ws.rs.GET;
import jakarta.ws.rs.Path;
import jakarta.ws.rs.Produces;
import jakarta.ws.rs.QueryParam;
import jakarta.ws.rs.core.MediaType;
import org.eclipse.microprofile.openapi.annotations.Operation;
import su.svn.api.domain.enums.ResourcePath;
import su.svn.api.model.dto.Page;
import su.svn.api.model.dto.RecordData;
import su.svn.api.services.domain.RecordDataService;
import su.svn.api.services.mappers.PageRecordDataMapper;

@Path(ResourcePath.RECORDS)
public class RecordResource {

    @Inject
    PageRecordDataMapper pageRecordDataMapper;

    @Inject
    RecordDataService recordDataService;

    @Operation(summary = "Get page with list of JSON record")
    @GET
    @Produces(MediaType.APPLICATION_JSON)
    public Uni<Page<RecordData>> page(@QueryParam("page") int page, @QueryParam("size") byte size) {
        return recordDataService.readPage(page, size)
                .map(postRecordPage -> pageRecordDataMapper.toPage(postRecordPage));
    }
}
