/*
 * This file was last modified at 2026.05.22 18:49 by Victor N. Skurikhin.
 * This is free and unencumbered software released into the public domain.
 * For more information, please refer to <http://unlicense.org>
 * JsonRecordMapper.java
 * $Id$
 */

package su.svn.api.services.mappers;

import org.mapstruct.Mapper;
import org.mapstruct.Mapping;
import org.mapstruct.MappingConstants;
import org.mapstruct.ValueMapping;
import su.svn.api.domain.entities.PostRecord;
import su.svn.api.models.dto.EntityModelResourceRecordView;
import su.svn.api.models.dto.RecordView;
import su.svn.api.models.dto.ResourceJsonRecord;
import su.svn.api.models.dto.UpdateJsonRecord;

@Mapper(componentModel = "cdi")
public interface JsonRecordMapper extends DateTimeMapper {
    @ValueMapping(source = "UNRECOGNIZED", target = MappingConstants.NULL)
    @Mapping(target = "parent", ignore = true)
    @Mapping(target = "sequenceId", ignore = true)
    @Mapping(target = "userName", constant = "root")
    @Mapping(target = "createTime", ignore = true)
    @Mapping(target = "updateTime", ignore = true)
    @Mapping(target = "enabled", ignore = true)
    @Mapping(target = "localChange", ignore = true)
    PostRecord toEntity(EntityModelResourceRecordView recordView);

    @ValueMapping(source = "UNRECOGNIZED", target = MappingConstants.NULL)
    @Mapping(target = "parent", ignore = true)
    @Mapping(target = "sequenceId", ignore = true)
    @Mapping(target = "userName", constant = "root")
    @Mapping(target = "createTime", ignore = true)
    @Mapping(target = "updateTime", ignore = true)
    @Mapping(target = "lastChangedTime", ignore = true)
    @Mapping(target = "enabled", ignore = true)
    @Mapping(target = "localChange", ignore = true)
    @Mapping(target = "blob", ignore = true)
    @Mapping(target = "texts", ignore = true)
    @Mapping(target = "fileName", ignore = true)
    @Mapping(target = "html", ignore = true)
    @Mapping(target = "link", ignore = true)
    @Mapping(target = "markdown", ignore = true)
    @Mapping(target = "value", ignore = true)
    PostRecord toEntity(UpdateJsonRecord updateJsonRecord);

    @ValueMapping(source = "UNRECOGNIZED", target = MappingConstants.NULL)
    ResourceJsonRecord toResource(PostRecord postRecord);

    // DEPRECATED
    @Deprecated
    @ValueMapping(source = "UNRECOGNIZED", target = MappingConstants.NULL)
    @Mapping(target = "parent", ignore = true)
    @Mapping(target = "sequenceId", ignore = true)
    @Mapping(target = "type", ignore = true)
    @Mapping(target = "userName", constant = "root")
    @Mapping(target = "createTime", ignore = true)
    @Mapping(target = "updateTime", ignore = true)
    @Mapping(target = "enabled", ignore = true)
    @Mapping(target = "localChange", ignore = true)
    @Mapping(source = "values", target = "json")
    @Mapping(target = "blob", ignore = true)
    @Mapping(target = "texts", ignore = true)
    @Mapping(target = "fileName", ignore = true)
    @Mapping(target = "html", ignore = true)
    @Mapping(target = "link", ignore = true)
    @Mapping(target = "markdown", ignore = true)
    @Mapping(target = "value", ignore = true)
    PostRecord toEntity(RecordView recordView);
}
