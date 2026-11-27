FROM mcr.microsoft.com/dotnet/sdk:10.0-noble AS build
WORKDIR /src
COPY bench.csproj ./
RUN dotnet restore -r linux-x64
COPY Program.cs ./
RUN dotnet publish -c Release -r linux-x64 --no-restore --self-contained false -o /out

# Chiseled: the ASP.NET runtime and its native dependencies, no shell, non-root.
FROM mcr.microsoft.com/dotnet/aspnet:10.0-noble-chiseled
WORKDIR /app
COPY --from=build /out ./
USER 65532:65532
ENTRYPOINT ["dotnet", "bench.dll"]
