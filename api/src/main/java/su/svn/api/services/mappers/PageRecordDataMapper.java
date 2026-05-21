/*
 * This file was last modified at 2026.05.21 16:49 by Victor N. Skurikhin.
 * This is free and unencumbered software released into the public domain.
 * For more information, please refer to <http://unlicense.org>
 * PageRecordDataMapper.java
 * $Id$
 */

package su.svn.api.services.mappers;

import org.mapstruct.Mapper;
import org.mapstruct.MappingConstants;
import org.mapstruct.ValueMapping;
import su.svn.api.domain.entities.PostRecord;
import su.svn.api.models.dto.Page;
import su.svn.api.models.dto.RecordData;

@Mapper(componentModel = "cdi")
public interface PageRecordDataMapper {
    @ValueMapping(source = "UNRECOGNIZED", target = MappingConstants.NULL)
    Page<RecordData> toPage(Page<PostRecord> record);
}
