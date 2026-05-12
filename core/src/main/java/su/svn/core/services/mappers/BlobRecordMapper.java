/*
 * This file was last modified at 2026.05.08 11:39 by Victor N. Skurikhin.
 * This is free and unencumbered software released into the public domain.
 * For more information, please refer to <http://unlicense.org>
 * BlobRecordMapper.java
 * $Id$
 */

package su.svn.core.services.mappers;

import org.mapstruct.Mapper;
import org.mapstruct.Mapping;
import org.mapstruct.MappingConstants;
import org.mapstruct.ValueMapping;
import su.svn.core.domain.entities.BlobRecord;
import su.svn.core.domain.entities.Tag;
import su.svn.core.models.dto.NewBlobRecord;
import su.svn.core.models.dto.ResourceBlobRecord;
import su.svn.core.models.dto.UpdateBlobRecord;

@Mapper(
        componentModel = "spring",
        unmappedTargetPolicy = org.mapstruct.ReportingPolicy.IGNORE,
        uses = {BaseRecordMapper.class}
)
public interface BlobRecordMapper {
    @ValueMapping(source = "UNRECOGNIZED", target = MappingConstants.NULL)
    @Mapping(source = "baseRecord.parentId", target = "parentId")
    @Mapping(source = "baseRecord.postAt", target = "postAt")
    @Mapping(source = "baseRecord.refreshAt", target = "refreshAt")
    @Mapping(source = "baseRecord.tags", target = "tags")
    ResourceBlobRecord toResource(BlobRecord record);

    @Mapping(target = "id", ignore = true)
    @Mapping(target = "userName", ignore = true)
    @Mapping(target = "refreshAt", ignore = true)
    ResourceBlobRecord toResource(NewBlobRecord record);

    @Mapping(target = "userName", ignore = true)
    ResourceBlobRecord toResource(UpdateBlobRecord record);

    @Mapping(target = "baseRecord", ignore = true)
    @Mapping(target = "createTime", ignore = true)
    @Mapping(target = "updateTime", ignore = true)
    @Mapping(target = "enabled", ignore = true)
    @Mapping(target = "localChange", ignore = true)
    @Mapping(source = "id", target = "id")
    @Mapping(source = "id", target = "baseRecord.id")
    @Mapping(source = "parentId", target = "baseRecord.parentId")
    @Mapping(source = "postAt", target = "baseRecord.postAt")
    @Mapping(source = "refreshAt", target = "baseRecord.refreshAt")
    @Mapping(source = "tags", target = "baseRecord.tags")
    BlobRecord toEntity(ResourceBlobRecord record);

    default Tag map(String value) {
        return Tag.builder()
                .tag(value)
                .build();
    }

    default String map(Tag value) {
        return value.tag();
    }
}
