/*
 * This file was last modified at 2026.05.21 23:42 by Victor N. Skurikhin.
 * This is free and unencumbered software released into the public domain.
 * For more information, please refer to <http://unlicense.org>
 * SetPostRecordMapper.java
 * $Id$
 */

package su.svn.api.services.mappers;

import org.mapstruct.Mapper;
import org.mapstruct.Mapping;
import org.mapstruct.MappingConstants;
import org.mapstruct.ValueMapping;
import su.svn.api.domain.entities.PostRecord;
import su.svn.api.models.dto.ResourceSetRecord;
import su.svn.api.models.dto.UpdateSetRecord;

@Mapper(componentModel = "cdi")
public interface SetPostRecordMapper extends DateTimeMapper {
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
    @Mapping(target = "json", ignore = true)
    PostRecord toEntity(UpdateSetRecord setRecord);

    @ValueMapping(source = "UNRECOGNIZED", target = MappingConstants.NULL)
    ResourceSetRecord toResource(PostRecord postRecord);
}
