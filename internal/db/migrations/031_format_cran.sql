-- +goose Up
ALTER TABLE repositories DROP CONSTRAINT IF EXISTS repositories_format_check;
ALTER TABLE repositories ADD CONSTRAINT repositories_format_check CHECK (format IN (
    'maven2','npm','docker','oci','pypi','go','nuget','helm','raw',
    'apt','yum','cargo','conan','conda','terraform','rubygems','cran'
));

-- +goose Down
ALTER TABLE repositories DROP CONSTRAINT IF EXISTS repositories_format_check;
ALTER TABLE repositories ADD CONSTRAINT repositories_format_check CHECK (format IN (
    'maven2','npm','docker','oci','pypi','go','nuget','helm','raw',
    'apt','yum','cargo','conan','conda','terraform','rubygems'
));
