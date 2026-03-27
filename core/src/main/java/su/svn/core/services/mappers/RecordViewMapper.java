/*
 * This file was last modified at 2026.03.27 14:01 by Victor N. Skurikhin.
 * This is free and unencumbered software released into the public domain.
 * For more information, please refer to <http://unlicense.org>
 * RecordViewMapper.java
 * $Id$
 */

package su.svn.core.services.mappers;

import org.mapstruct.Mapper;
import org.mapstruct.MappingConstants;
import org.mapstruct.ValueMapping;
import su.svn.core.domain.entities.RecordView;
import su.svn.core.models.dto.ResourceRecordView;

/**
 * Mapper for converting {@link su.svn.core.domain.entities.RecordView}
 * to {@link su.svn.core.models.dto.ResourceRecordView}.
 */
@Mapper(
        componentModel = "spring",
        unmappedTargetPolicy = org.mapstruct.ReportingPolicy.IGNORE,
        uses = {BaseRecordMapper.class}
)
public interface RecordViewMapper {
    @ValueMapping(source = "UNRECOGNIZED", target = MappingConstants.NULL)
    ResourceRecordView toResource(RecordView record);
}
