/*
 * This file was last modified at 2026.05.23 11:55 by Victor N. Skurikhin.
 * This is free and unencumbered software released into the public domain.
 * For more information, please refer to <http://unlicense.org>
 * PostRecordMapper.java
 * $Id$
 */

package su.svn.api.services.mappers;

import org.mapstruct.Mapper;
import org.mapstruct.Mapping;
import org.mapstruct.MappingTarget;
import org.mapstruct.Named;
import org.mapstruct.factory.Mappers;
import su.svn.api.domain.entities.PostRecord;

@Mapper(componentModel = "cdi")
public interface PostRecordMapper {
    PostRecordMapper INSTANCE = Mappers.getMapper(PostRecordMapper.class);

    @Named("updateRecord")
    @Mapping(target = "parent", qualifiedByName = "updateRecord")
    @Mapping(target = "sequenceId", ignore = true)
    @Mapping(target = "lastChangedTime", ignore = true)
    void update(@MappingTarget PostRecord to, PostRecord from);

    @Named("updateExisting")
    @Mapping(target = "id", ignore = true)
    @Mapping(target = "sequenceId", ignore = true)
    void updateExistingRecord(@MappingTarget PostRecord to, PostRecord from);
}
